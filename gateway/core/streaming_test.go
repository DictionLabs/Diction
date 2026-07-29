package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// --- proxyToBackend ---

func TestProxyToBackend_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello from backend"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)
	pcm := make([]byte, 3200) // 0.1s of silence at 16kHz 16-bit mono

	text, err := g.proxyToBackend(context.Background(), target, audioPayload{data: pcm, filename: "audio.wav"}, &Backend{Name: "small"}, "en")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if text != "hello from backend" {
		t.Errorf("text: want 'hello from backend', got %q", text)
	}
}

func TestProxyToBackend_NoLanguage(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"no lang"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	text, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "")
	if err != nil {
		t.Fatalf("proxyToBackend with empty language: %v", err)
	}
	if text != "no lang" {
		t.Errorf("got: %q", text)
	}
}

func TestProxyToBackend_BackendError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "")
	if err == nil {
		t.Fatal("expected error for backend 500")
	}
}

func TestProxyToBackend_InvalidJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not-json`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestProxyToBackend_UnreachableHost(t *testing.T) {
	g := testGateway()
	target, _ := url.Parse("http://127.0.0.1:1") // nothing listening there

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "")
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

// TestProxyToBackend_NoModelFieldWhenForwardModelEmpty is a regression test for
// the bug where streaming sent model=large-v3-turbo to the whisper backend,
// which attempted to download an invalid model and returned HTTP 500.
// The HTTP transcription path (rewriteMultipart) never injects the model field
// when ForwardModel is empty; proxyToBackend must match that behaviour.
func TestProxyToBackend_NoModelFieldWhenForwardModelEmpty(t *testing.T) {
	var receivedModel string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(10 << 20)
		receivedModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "large-v3-turbo", ForwardModel: ""}, "ko")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if receivedModel != "" {
		t.Errorf("model field sent to backend: want empty (use WHISPER__MODEL default), got %q", receivedModel)
	}
}

func TestProxyToBackend_InjectsForwardModelWhenSet(t *testing.T) {
	var receivedModel string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(10 << 20)
		receivedModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "large-v3-turbo", ForwardModel: "deepdml/faster-whisper-large-v3-turbo-ct2"}, "ko")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if receivedModel != "deepdml/faster-whisper-large-v3-turbo-ct2" {
		t.Errorf("model field: want %q, got %q", "deepdml/faster-whisper-large-v3-turbo-ct2", receivedModel)
	}
}

func TestProxyToBackend_UsesTargetPath(t *testing.T) {
	var requestedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "canary", TargetPath: "/inference"}, "en")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if requestedPath != "/inference" {
		t.Errorf("path: want /inference, got %q", requestedPath)
	}
}

func TestProxyToBackend_SendsAuthHeader(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "custom", AuthHeader: "Bearer secret-token"}, "en")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if receivedAuth != "Bearer secret-token" {
		t.Errorf("auth header: want 'Bearer secret-token', got %q", receivedAuth)
	}
}

// --- StreamingHandler ---

// startStreamingServer creates a Gateway with a mock whisper backend and
// returns a test HTTP server running the StreamingHandler, plus a cleanup func.
func startStreamingServer(t *testing.T, whisperResponse string, whisperStatus int) (*httptest.Server, *Gateway) {
	t.Helper()

	// Mock whisper backend
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(whisperStatus)
		if whisperStatus == http.StatusOK {
			fmt.Fprintf(w, `{"text":"%s"}`, whisperResponse)
		}
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: whisper.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)
	return srv, g
}

func wsURL(srv *httptest.Server, query string) string {
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/audio/stream"
	if query != "" {
		u += "?" + query
	}
	return u
}

func TestStreamingHandler_NoMatchingBackend(t *testing.T) {
	// defaultModel has no matching backend → HTTP 503 before WebSocket upgrade
	g := &Gateway{
		backends:     []Backend{},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	srv := httptest.NewServer(g.StreamingHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/audio/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", resp.StatusCode)
	}
}

func TestStreamingHandler_BackendUnavailable(t *testing.T) {
	srv, g := startStreamingServer(t, "ok", http.StatusOK)
	g.health.set("small", false) // mark as down

	resp, err := http.Get(srv.URL + "/v1/audio/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", resp.StatusCode)
	}
}

func TestStreamingHandler_HappyPath(t *testing.T) {
	srv, _ := startStreamingServer(t, "transcribed text", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small&language=en"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send PCM audio (silence)
	pcm := make([]byte, 3200)
	if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	// Send done action
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

	// Read transcription result
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("msg type: want text, got %v", msgType)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Text != "transcribed text" {
		t.Errorf("text: want 'transcribed text', got %q", result.Text)
	}
}

// TestStreamingHandler_OnTranscriptionReportsSTTTime pins the hook this path
// gained with the per-stage wait metrics. Before it, /v1/audio/stream never
// called OnTranscription at all, so every `stream` row in Influx carried
// output_chars=0 and audio_duration_ms=0 — and had nowhere to put stt_ms.
func TestStreamingHandler_OnTranscriptionReportsSTTTime(t *testing.T) {
	// The hook fires on the handler goroutine and is read on the test goroutine,
	// so every captured value is atomic — including the model string.
	var (
		gotModel    atomic.Value
		gotChars    atomic.Int64
		gotDuration atomic.Int64
		called      atomic.Bool
	)

	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"text":"hello there"}`)
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: whisper.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
		OnTranscription: func(_ context.Context, model string, _ int64, chars int, durationMs int64, _, _ bool) {
			gotModel.Store(model)
			gotChars.Store(int64(chars))
			gotDuration.Store(durationMs)
			called.Store(true)
		},
	}
	g.health.set("small", true)

	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small&language=en"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// 32000 bytes = exactly 1s at 16kHz / 16-bit / mono.
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 32000)); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read result: %v", err)
	}

	if !called.Load() {
		t.Fatal("OnTranscription was never called on the stream path")
	}
	if got, _ := gotModel.Load().(string); got != "small" {
		t.Errorf("model: want small, got %q", got)
	}
	if gotChars.Load() != int64(len("hello there")) {
		t.Errorf("chars: want %d, got %d", len("hello there"), gotChars.Load())
	}
	if gotDuration.Load() != 1000 {
		t.Errorf("durationMs: want 1000 for 32000 bytes of 16kHz mono s16, got %d", gotDuration.Load())
	}
}

func TestStreamingHandler_LanguageOverrideInDone(t *testing.T) {
	srv, _ := startStreamingServer(t, "override", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))

	// done action with language override
	done, _ := json.Marshal(map[string]string{"action": "done", "language": "fr"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "override" {
		t.Errorf("text: want 'override', got %q", result.Text)
	}
}

func TestStreamingHandler_NoAudio(t *testing.T) {
	srv, _ := startStreamingServer(t, "ok", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send done with no preceding audio
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	// Server should close with wsCloseNoAudio (4004)
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection to be closed by server")
	}
}

func TestStreamingHandler_AudioTooLarge(t *testing.T) {
	srv, g := startStreamingServer(t, "ok", http.StatusOK)
	g.maxBodySize = 100 // tiny limit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send more than maxBodySize bytes
	conn.Write(ctx, websocket.MessageBinary, make([]byte, 200))

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection closed due to oversized audio")
	}
}

func TestStreamingHandler_BackendTranscriptionFails(t *testing.T) {
	srv, _ := startStreamingServer(t, "", http.StatusInternalServerError)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection closed due to transcription failure")
	}
}

func TestStreamingHandler_DegenerateRepetition_ClosesAndReportsHallucination(t *testing.T) {
	// Not parallel-safe: mutates core.OnError.
	events, restore := withCapturedOnError(t)
	defer restore()

	repeated := strings.Repeat("tamb ", 20)
	srv, _ := startStreamingServer(t, repeated, http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	// Server should close the connection rather than deliver the
	// hallucinated transcript — same treatment as a genuine backend failure.
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection closed due to degenerate repetition")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(*events) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*events) == 0 {
		t.Fatal("no OnError event captured")
	}
	got := (*events)[0]
	if got.Kind != "stt_hallucination" {
		t.Errorf("kind: want stt_hallucination, got %q", got.Kind)
	}
}

func TestStreamingHandler_NonDoneTextMessage(t *testing.T) {
	// Non-"done" text messages are ignored; server keeps waiting.
	// We send a non-done message then a done to confirm normal flow still works.
	srv, _ := startStreamingServer(t, "after ignore", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	// Send a text message with unknown action - should be ignored
	conn.Write(ctx, websocket.MessageText, []byte(`{"action":"unknown"}`))
	// Now send done
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "after ignore" {
		t.Errorf("want 'after ignore', got %q", result.Text)
	}
}

func TestStreamingHandler_InvalidTextIgnored(t *testing.T) {
	// Non-JSON text → json.Unmarshal fails → continue; server keeps waiting.
	srv, _ := startStreamingServer(t, "after garbage", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	conn.Write(ctx, websocket.MessageText, []byte("not-json-at-all"))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "after garbage" {
		t.Errorf("want 'after garbage', got %q", result.Text)
	}
}

func TestStreamingHandler_HealthByAliasName(t *testing.T) {
	// When model query param is an alias whose case differs from the backend Name
	// (e.g. "SMALL" vs stored health key "small"), g.health.get(model) returns
	// false - but the alias-scan loop should find the backend is healthy and allow
	// the upgrade. Covers the backendUp=true branch inside the health-check block.
	srv, _ := startStreamingServer(t, "alias ok", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// "SMALL" is an alias for "small"; health key is stored as "small"
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=SMALL"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "alias ok" {
		t.Errorf("want 'alias ok', got %q", result.Text)
	}
}

func startStreamingServerWithPostProcess(t *testing.T, whisperResponse string, postProcess func(context.Context, string, string, string) (string, string, error)) *httptest.Server {
	t.Helper()
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"text":"%s"}`, whisperResponse)
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: whisper.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	srv := httptest.NewServer(g.StreamingHandlerWithPostProcess(postProcess))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamingHandler_WithPostProcess_Success(t *testing.T) {
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		return "cleaned: " + text, "transcribe", nil
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "enhance=true"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "cleaned: raw audio" {
		t.Errorf("want 'cleaned: raw audio', got %q", result.Text)
	}
}

func TestStreamingHandler_WithPostProcess_ErrorFallback(t *testing.T) {
	// postProcess error → raw transcript returned unchanged
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		return "", "", fmt.Errorf("llm error")
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "enhance=true"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "raw audio" {
		t.Errorf("want 'raw audio' fallback, got %q", result.Text)
	}
}

func TestStreamingHandler_DefaultModel(t *testing.T) {
	// No ?model param → uses default model
	srv, _ := startStreamingServer(t, "default model used", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, ""), nil) // no model param
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "default model used" {
		t.Errorf("want 'default model used', got %q", result.Text)
	}
}

func TestStreamingHandler_ContextAfterAudio_Dropped(t *testing.T) {
	// Context sent AFTER first audio binary frame should be silently dropped.
	// streaming.go:95 sets contextRead=true on any binary frame, so subsequent
	// non-action text frames are ignored. If iOS client ever reorders frames,
	// context vanishes with no error.
	var receivedContext string
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		receivedContext = contextJSON
		return "cleaned: " + text, "transcribe", nil
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "enhance=true"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send audio FIRST (sets contextRead = true)
	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))

	// Send context AFTER audio — should be silently dropped
	contextJSON := `{"before":"hello ","after":" world"}`
	conn.Write(ctx, websocket.MessageText, []byte(contextJSON))

	// Send done
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "cleaned: raw audio" {
		t.Errorf("want 'cleaned: raw audio', got %q", result.Text)
	}
	if receivedContext != "" {
		t.Errorf("context sent after audio should be dropped, got %q", receivedContext)
	}
}

func TestStreamingHandler_FirstTextFrameAsContext(t *testing.T) {
	var receivedContext string
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		receivedContext = contextJSON
		return "cleaned: " + text, "transcribe", nil
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "enhance=true"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send context JSON as first text frame
	contextJSON := `{"before":"hello ","after":" world","selected":"test"}`
	conn.Write(ctx, websocket.MessageText, []byte(contextJSON))

	// Send audio
	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))

	// Send done
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "cleaned: raw audio" {
		t.Errorf("want 'cleaned: raw audio', got %q", result.Text)
	}
	if receivedContext != contextJSON {
		t.Errorf("context: want %q, got %q", contextJSON, receivedContext)
	}
}

func TestStreamingHandler_WithPostProcess_ModeInResponse(t *testing.T) {
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		if intent == "edit" {
			return "edited text", "edit", nil
		}
		return "cleaned: " + text, "transcribe", nil
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test with intent=edit
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "enhance=true&intent=edit"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct {
		Text string `json:"text"`
		Mode string `json:"mode"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "edited text" {
		t.Errorf("text: want 'edited text', got %q", result.Text)
	}
	if result.Mode != "edit" {
		t.Errorf("mode: want 'edit', got %q", result.Mode)
	}
}

func TestStreamingHandler_NoEnhance_NoModeField(t *testing.T) {
	// Without ?enhance=true, mode should be omitted (empty)
	srv, _ := startStreamingServer(t, "plain text", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, ""), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Verify mode is NOT in the JSON (omitempty)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, exists := raw["mode"]; exists {
		t.Error("mode field should be omitted when enhance is not used")
	}
}

// --- Commit A: observability (reason tag + device hash on ws_read) ---

// withCapturedOnError installs a temporary core.OnError capturing events into
// the returned slice; the returned restore func puts the prior hook back.
// Not parallel-safe — the hook is a package-global.
func withCapturedOnError(t *testing.T) (*[]ErrorEvent, func()) {
	t.Helper()
	var events []ErrorEvent
	prev := OnError
	OnError = func(_ context.Context, e ErrorEvent) {
		events = append(events, e)
	}
	return &events, func() { OnError = prev }
}

func TestStreamingHandler_EmitsReasonOnWSRead_GoingAway(t *testing.T) {
	// Not parallel-safe: mutates core.OnError.
	events, restore := withCapturedOnError(t)
	defer restore()

	srv, _ := startStreamingServer(t, "ok", http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Cleanly close client-side with StatusGoingAway — mirrors iOS extension
	// being backgrounded cleanly. The server should emit ws_read with
	// reason=going_away.
	_ = conn.Close(websocket.StatusGoingAway, "")

	// Give the server a moment to observe the close and emit the event.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(*events) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(*events) == 0 {
		t.Fatal("no OnError event captured")
	}
	got := (*events)[0]
	if got.Kind != "ws_read" {
		t.Errorf("kind: want ws_read, got %q", got.Kind)
	}
	if got.Reason != wsReasonGoingAway {
		t.Errorf("reason: want %q, got %q", wsReasonGoingAway, got.Reason)
	}
}

// --- Commit B: idle timeout + clean close frame ---

// TestStreamingHandler_IdleTimeout_ClosesClient verifies the per-frame idle
// deadline closes zombie sockets instead of holding them up to the 90-min cap.
// Not parallel-safe (mutates core.OnError).
func TestStreamingHandler_IdleTimeout_ClosesClient(t *testing.T) {
	events, restore := withCapturedOnError(t)
	defer restore()

	srv, g := startStreamingServer(t, "ok", http.StatusOK)
	g.streamIdleTimeout = 300 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send nothing. Server should close within idle + a little slack.
	start := time.Now()
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected read error after server closed")
	}
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("close took too long: %v", elapsed)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CloseError, got %T: %v", err, err)
	}
	if ce.Code != websocket.StatusPolicyViolation {
		t.Errorf("close code: want %d, got %d", websocket.StatusPolicyViolation, ce.Code)
	}
	if ce.Reason != "idle_timeout" {
		t.Errorf("close reason: want idle_timeout, got %q", ce.Reason)
	}
	// Server-side OnError should have fired with reason=idle_timeout.
	// Give the handler goroutine a moment to fully emit before asserting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(*events) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*events) == 0 {
		t.Fatal("no OnError emitted for idle timeout")
	}
	if (*events)[0].Reason != wsReasonIdleTimeout {
		t.Errorf("emitted reason: want %q, got %q", wsReasonIdleTimeout, (*events)[0].Reason)
	}
}

func TestStreamingHandler_IdleTimeout_HappyPathUnaffected(t *testing.T) {
	// Healthy streams (frames within idle) must still succeed end-to-end.
	srv, g := startStreamingServer(t, "alive", http.StatusOK)
	g.streamIdleTimeout = 2 * time.Second // generous enough for test round-trip

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result struct{ Text string }
	json.Unmarshal(data, &result)
	if result.Text != "alive" {
		t.Errorf("want 'alive', got %q", result.Text)
	}
}

func TestNewGateway_DefaultsIdleTimeout(t *testing.T) {
	g := NewGateway(Config{Backends: []Backend{{Name: "x", URL: "http://localhost"}}, DefaultModel: "x"})
	if g.streamIdleTimeout != defaultStreamIdleTimeout {
		t.Errorf("default idle: want %v, got %v", defaultStreamIdleTimeout, g.streamIdleTimeout)
	}
}

func TestNewGateway_RespectsIdleTimeoutConfig(t *testing.T) {
	g := NewGateway(Config{
		Backends:          []Backend{{Name: "x", URL: "http://localhost"}},
		DefaultModel:      "x",
		StreamIdleTimeout: 3 * time.Second,
	})
	if g.streamIdleTimeout != 3*time.Second {
		t.Errorf("idle: want 3s, got %v", g.streamIdleTimeout)
	}
}

// --- Commit C: OnRequestFailed hook ---

// withCapturedOnRequestFailed installs a temporary core.OnRequestFailed that
// appends each (errorType) call to the returned slice. Not parallel-safe.
func withCapturedOnRequestFailed(t *testing.T) (*[]string, func()) {
	t.Helper()
	var calls []string
	prev := OnRequestFailed
	OnRequestFailed = func(_ context.Context, errorType string) {
		calls = append(calls, errorType)
	}
	return &calls, func() { OnRequestFailed = prev }
}

func TestStreamingHandler_WSReadError_TriggersOnRequestFailed(t *testing.T) {
	// Not parallel-safe: mutates core.OnRequestFailed.
	calls, restore := withCapturedOnRequestFailed(t)
	defer restore()

	srv, _ := startStreamingServer(t, "ok", http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close(websocket.StatusGoingAway, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(*calls) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*calls) == 0 {
		t.Fatal("OnRequestFailed not called on ws_read error")
	}
	if (*calls)[0] != errTypeSTTError {
		t.Errorf("errorType: want %q, got %q", errTypeSTTError, (*calls)[0])
	}
}

func TestStreamingHandler_NoAudio_TriggersOnRequestFailed(t *testing.T) {
	calls, restore := withCapturedOnRequestFailed(t)
	defer restore()

	srv, _ := startStreamingServer(t, "ok", http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
	_, _, _ = conn.Read(ctx) // wait for server close

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(*calls) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*calls) == 0 {
		t.Fatal("OnRequestFailed not called for no-audio path")
	}
	if (*calls)[0] != errTypeSTTError {
		t.Errorf("errorType: want %q, got %q", errTypeSTTError, (*calls)[0])
	}
}

func TestStreamingHandler_BackendFailure_TriggersOnRequestFailed(t *testing.T) {
	calls, restore := withCapturedOnRequestFailed(t)
	defer restore()

	srv, _ := startStreamingServer(t, "", http.StatusInternalServerError)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_ = conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
	_, _, _ = conn.Read(ctx) // wait for server close

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(*calls) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*calls) == 0 {
		t.Fatal("OnRequestFailed not called on backend failure")
	}
	if (*calls)[0] != errTypeSTTError {
		t.Errorf("errorType: want %q, got %q", errTypeSTTError, (*calls)[0])
	}
}

func TestStreamingHandler_HappyPath_NoRequestFailed(t *testing.T) {
	// Successful stream must NOT call OnRequestFailed.
	calls, restore := withCapturedOnRequestFailed(t)
	defer restore()

	srv, _ := startStreamingServer(t, "transcribed", http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_ = conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}

	// Give any stray goroutine a moment to (wrongly) fire.
	time.Sleep(100 * time.Millisecond)
	if len(*calls) != 0 {
		t.Errorf("OnRequestFailed fired on happy path: %v", *calls)
	}
}

func TestClassifyWSError_Cases(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"going away CloseError", websocket.CloseError{Code: websocket.StatusGoingAway}, wsReasonGoingAway},
		{"protocol error", websocket.CloseError{Code: websocket.StatusProtocolError}, wsReasonProtocol},
		{"unsupported data", websocket.CloseError{Code: websocket.StatusUnsupportedData}, wsReasonProtocol},
		{"stream deadline", context.DeadlineExceeded, wsReasonStreamTimeout},
		{"canceled", context.Canceled, wsReasonStreamTimeout},
		{"io.EOF", io.EOF, wsReasonEOF},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, wsReasonEOF},
		{"wrapped EOF message", errors.New("failed to read frame header: EOF"), wsReasonEOF},
		{"going away string", errors.New("going away from server"), wsReasonGoingAway},
		{"StatusGoingAway string", errors.New("StatusGoingAway received"), wsReasonGoingAway},
		{"protocol string", errors.New("bad protocol frame"), wsReasonProtocol},
		{"unknown", errors.New("something else entirely"), wsReasonUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyWSError(tc.err); got != tc.want {
				t.Errorf("ClassifyWSError(%v): want %q, got %q", tc.err, tc.want, got)
			}
		})
	}
}

// --- StreamingHandler auto-detect routing ---

func TestStreamingHandler_AutoDetect_WithDeviceHash(t *testing.T) {
	// Verify that auto-detect routing wires DeviceHashFromContext and profileStore.
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"auto-detect result"}`)
	}))
	defer whisper.Close()

	whisperURL, _ := url.Parse(whisper.URL)
	_ = whisperURL

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: whisper.URL, Aliases: []string{"small"}},
		},
		health:        newHealthState(),
		defaultModel:  "small",
		fallbackModel: "small", // enables ModelForAutoDetect to return non-empty
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("small", true)

	// Wire DeviceHashFromContext so the inner block (lines 139-141) is executed
	g.DeviceHashFromContext = func(ctx context.Context) string {
		return "test-device-hash-abc123"
	}
	// Wire profileStore (cold start → no profile → whisper_safe tier)
	var dbPtr atomic.Pointer[sql.DB]
	g.profileStore = NewProfileStore(&dbPtr)

	srv := httptest.NewServer(g.StreamingHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=auto"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	pcm := make([]byte, 3200)
	if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Text != "auto-detect result" {
		t.Errorf("text: want 'auto-detect result', got %q", result.Text)
	}
}
