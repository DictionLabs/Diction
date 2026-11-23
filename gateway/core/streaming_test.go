package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

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
	pcm := make([]byte, 3200)

	text, err := g.proxyToBackend(context.Background(), target, audioPayload{data: pcm, filename: "audio.wav"}, &Backend{Name: "small"}, "en", "", "")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if text != "hello from backend" {
		t.Errorf("text: want 'hello from backend', got %q", text)
	}
}

func TestProxyToBackend_NoLanguage(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"no lang"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	text, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "", "", "")
	if err != nil {
		t.Fatalf("proxyToBackend with empty language: %v", err)
	}
	if text != "no lang" {
		t.Errorf("got: %q", text)
	}
}

func TestProxyToBackend_StripsPromptEcho(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"The following words are spelled exactly as shown, Grafana, Grafana"}`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)
	prompt := buildWhisperPrompt(`{"customWords":[{"word":"Grafana"}]}`)
	text, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "en", prompt, "")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if text != "" {
		t.Errorf("echoed prompt returned: %q", text)
	}
}

func TestProxyToBackend_BackendError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "", "", "")
	if err == nil {
		t.Fatal("expected error for backend 500")
	}
}

func TestProxyToBackend_InvalidJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not-json`)
	}))
	defer backend.Close()

	g := testGateway()
	target, _ := url.Parse(backend.URL)

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "", "", "")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestProxyToBackend_UnreachableHost(t *testing.T) {
	g := testGateway()
	target, _ := url.Parse("http://127.0.0.1:1")

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 100), filename: "audio.wav"}, &Backend{Name: "small"}, "", "", "")
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

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

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "large-v3-turbo", ForwardModel: ""}, "ko", "", "")
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

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "large-v3-turbo", ForwardModel: "deepdml/faster-whisper-large-v3-turbo-ct2"}, "ko", "", "")
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

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "canary", TargetPath: "/inference"}, "en", "", "")
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

	_, err := g.proxyToBackend(context.Background(), target, audioPayload{data: make([]byte, 3200), filename: "audio.wav"}, &Backend{Name: "custom", AuthHeader: "Bearer secret-token"}, "en", "", "")
	if err != nil {
		t.Fatalf("proxyToBackend: %v", err)
	}
	if receivedAuth != "Bearer secret-token" {
		t.Errorf("auth header: want 'Bearer secret-token', got %q", receivedAuth)
	}
}

func startStreamingServer(t *testing.T, whisperResponse string, whisperStatus int) (*httptest.Server, *Gateway) {
	t.Helper()

	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
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
	g := &Gateway{
		backends:     []Backend{},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
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
	g.health.set("small", false)

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

	pcm := make([]byte, 3200)
	if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

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

func TestStreamingHandler_OnTranscriptionReportsSTTTime(t *testing.T) {
	var (
		gotModel    atomic.Value
		gotChars    atomic.Int64
		gotDuration atomic.Int64
		called      atomic.Bool
	)

	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small&language=en"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

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

	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection to be closed by server")
	}
}

func TestStreamingHandler_AudioTooLarge(t *testing.T) {
	srv, g := startStreamingServer(t, "ok", http.StatusOK)
	g.maxBodySize = 100

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

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

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection closed due to degenerate repetition")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Len() == 0 {
		t.Fatal("no OnError event captured")
	}
	got := events.All()[0]
	if got.Kind != "stt_hallucination" {
		t.Errorf("kind: want stt_hallucination, got %q", got.Kind)
	}
}

func TestStreamingHandler_NonDoneTextMessage(t *testing.T) {
	srv, _ := startStreamingServer(t, "after ignore", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	conn.Write(ctx, websocket.MessageText, []byte(`{"action":"unknown"}`))
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
	srv, _ := startStreamingServer(t, "alias ok", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	srv := httptest.NewServer(trackHandler(g.StreamingHandlerWithPostProcess(postProcess)))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamingHandler_WithPostProcess_Success(t *testing.T) {
	postProcess := func(_ context.Context, text, _, _ string) (string, string, error) {
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
	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
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
	srv, _ := startStreamingServer(t, "default model used", http.StatusOK)

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
	var result struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &result)
	if result.Text != "default model used" {
		t.Errorf("want 'default model used', got %q", result.Text)
	}
}

func TestStreamingHandler_ContextAfterAudio_Dropped(t *testing.T) {
	var receivedContext string
	postProcess := func(_ context.Context, text, contextJSON, _ string) (string, string, error) {
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

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))

	contextJSON := `{"before":"hello ","after":" world"}`
	conn.Write(ctx, websocket.MessageText, []byte(contextJSON))

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
	postProcess := func(_ context.Context, text, contextJSON, _ string) (string, string, error) {
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

	contextJSON := `{"before":"hello ","after":" world","selected":"test"}`
	conn.Write(ctx, websocket.MessageText, []byte(contextJSON))

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
	if receivedContext != contextJSON {
		t.Errorf("context: want %q, got %q", contextJSON, receivedContext)
	}
}

type capturedPrompt struct {
	value   string
	present bool
}

func captureStreamingPrompt(t *testing.T, contextJSON string) capturedPrompt {
	t.Helper()

	receivedPrompt := make(chan capturedPrompt, 1)
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		values, present := r.MultipartForm.Value["prompt"]
		var value string
		if len(values) > 0 {
			value = values[0]
		}
		receivedPrompt <- capturedPrompt{value: value, present: present}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"text":"raw audio"}`)
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
	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=en"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if err := conn.Write(ctx, websocket.MessageText, []byte(contextJSON)); err != nil {
		t.Fatalf("write context: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}

	select {
	case prompt := <-receivedPrompt:
		return prompt
	case <-ctx.Done():
		t.Fatal("timed out waiting for backend prompt")
		return capturedPrompt{}
	}
}

func TestStreamingHandler_ForwardsCustomWordsAsPrompt(t *testing.T) {
	prompt := captureStreamingPrompt(t, `{"customWords":[{"word":"Kubernetes"},{"word":"PostgreSQL"}]}`)
	if !prompt.present {
		t.Fatal("prompt field omitted")
	}
	wantPrompt := "The following words are spelled exactly as shown: Kubernetes, PostgreSQL."
	if prompt.value != wantPrompt {
		t.Errorf("prompt: want %q, got %q", wantPrompt, prompt.value)
	}
}

func TestStreamingHandler_OmitsPromptWithoutCustomWords(t *testing.T) {
	prompt := captureStreamingPrompt(t, `{"customWords":[]}`)
	if prompt.present {
		t.Errorf("prompt field present with empty custom words: %q", prompt.value)
	}
}

func TestStreamingHandler_WithPostProcess_ModeInResponse(t *testing.T) {
	postProcess := func(_ context.Context, text, _, intent string) (string, string, error) {
		if intent == "edit" {
			return "edited text", "edit", nil
		}
		return "cleaned: " + text, "transcribe", nil
	}
	srv := startStreamingServerWithPostProcess(t, "raw audio", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, exists := raw["mode"]; exists {
		t.Error("mode field should be omitted when enhance is not used")
	}
}

func TestStreamingHandler_AutoDetect_WithDeviceHash(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		fallbackModel: "small",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("small", true)

	g.DeviceHashFromContext = func(_ context.Context) string {
		return "test-device-hash-abc123"
	}
	var dbPtr atomic.Pointer[sql.DB]
	g.profileStore = NewProfileStore(&dbPtr)

	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
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
