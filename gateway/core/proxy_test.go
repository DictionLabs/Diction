package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func parseMediaType(ct string) (string, map[string]string, error) {
	parts := strings.SplitN(ct, ";", 2)
	params := make(map[string]string)
	if len(parts) == 2 {
		for _, p := range strings.Split(parts[1], ";") {
			p = strings.TrimSpace(p)
			kv := strings.SplitN(p, "=", 2)
			if len(kv) == 2 {
				params[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
			}
		}
	}
	return strings.TrimSpace(parts[0]), params, nil
}

func TestTranscriptionHandler_MethodNotAllowed(t *testing.T) {
	g := testGateway()
	req := httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions", nil)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_NoMatchingBackend(t *testing.T) {
	g := &Gateway{
		backends:     []Backend{},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "data")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_BodyTooLarge(t *testing.T) {
	g := &Gateway{
		backends:     testGateway().backends,
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "this-is-way-too-long")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: want 413, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_FallbackModelRouteLogs(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"routed"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "canary-v2", URL: backend.URL, Aliases: []string{"canary-v2"}},
			{Name: "large-v3-turbo", URL: backend.URL, Aliases: []string{"large-v3-turbo"}},
		},
		health:        newHealthState(),
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("canary-v2", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_AutoDetectRoutesToFallback(t *testing.T) {
	var upstreamLanguage string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("backend: read multipart: %v", err)
		}
		for {
			part, perr := mr.NextPart()
			if perr != nil {
				break
			}
			if part.FormName() == "language" {
				b, _ := io.ReadAll(part)
				upstreamLanguage = string(b)
			}
			part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"detected"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "canary-v2", URL: backend.URL, Aliases: []string{"canary-v2"}},
			{Name: "large-v3-turbo", URL: backend.URL, Aliases: []string{"large-v3-turbo"}},
		},
		health:        newHealthState(),
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)

	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if upstreamLanguage != "" {
		t.Errorf("language must be stripped upstream when auto-detect is active; got %q", upstreamLanguage)
	}
	if got := rr.Header().Get("X-Diction-Route-Model"); got != "large-v3-turbo" {
		t.Errorf("Route-Model: want large-v3-turbo, got %q", got)
	}
	if got := rr.Header().Get("X-Diction-Route-Detect"); got != "true" {
		t.Errorf("Route-Detect: want true, got %q", got)
	}
}

func TestTranscriptionHandler_OnTranscriptionCallback(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"callback test"}`)
	}))
	defer backend.Close()

	var gotModel string
	var gotChars int
	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
		OnTranscription: func(_ context.Context, model string, _ int64, chars int, _ int64, _, _ bool) {
			gotModel = model
			gotChars = chars
		},
	}
	g.health.set("small", true)

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(func(_ context.Context, text, _, _ string) (string, string, error) {
		return text, "transcribe", nil
	})(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
	if gotModel != "small" {
		t.Errorf("OnTranscription model: want small, got %q", gotModel)
	}
	if gotChars == 0 {
		t.Errorf("OnTranscription chars: want >0, got 0")
	}
}

func TestTranscriptionHandler_ProxiesToBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello world"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hello world") {
		t.Errorf("expected transcription in body, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_WAVConversionFails(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "parakeet-v3", URL: "http://parakeet:5092", Aliases: []string{"parakeet-v3"}, NeedsWAV: true},
		},
		health:       newHealthState(),
		defaultModel: "parakeet-v3",
		maxBodySize:  10 * 1024 * 1024,
	}

	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 64)
	body, ct := buildMultipart(t, map[string]string{"model": "parakeet-v3"}, "audio.m4a", string(garbage))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestTranscriptionHandler_WithPostProcess_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw transcript"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	postProcess := func(_ context.Context, text, _, _ string) (string, string, error) {
		return "cleaned: " + text, "transcribe", nil
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cleaned: raw transcript") {
		t.Errorf("expected cleaned transcript, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_WithPostProcess_ErrorFallback(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw transcript"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("llm error")
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "raw transcript") {
		t.Errorf("expected raw transcript fallback, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_ModelStrippedBeforeProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse failed", 400)
			return
		}
		if r.FormValue("model") != "" {
			http.Error(w, "model field should not be present", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small", "language": "en"}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestTranscriptionHandler_ContextFieldPassedToPostProcess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	var receivedContext string
	postProcess := func(_ context.Context, text, contextJSON, _ string) (string, string, error) {
		receivedContext = contextJSON
		return text, "", nil
	}

	contextJSON := `{"before":"hello ","after":" world","selected":"test"}`
	body, ct := buildMultipart(t, map[string]string{"context": contextJSON}, "audio.wav", "RIFF"+string(make([]byte, 100)))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if receivedContext != contextJSON {
		t.Errorf("context: want %q, got %q", contextJSON, receivedContext)
	}
}

func TestTranscriptionHandler_UpstreamCanceled_EmitsError(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	failures, restoreFail := withCapturedOnRequestFailed(t)
	defer restoreFail()

	blockStart := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(blockStart) })
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	defer backend.Close()
	defer close(release)

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.TranscriptionHandler()(rr, req)
	}()

	<-blockStart
	cancel()
	<-done

	if events.Len() == 0 {
		t.Fatal("no OnError event captured on proxy cancel")
	}
	got := events.All()[0]
	if got.Kind != "stt_upstream_canceled" {
		t.Errorf("kind: want stt_upstream_canceled, got %q", got.Kind)
	}
	if got.Source != "stt" {
		t.Errorf("source: want stt, got %q", got.Source)
	}

	for _, e := range events.All() {
		if e.Kind == "stt_backend_5xx" {
			t.Errorf("client cancel wrongly emitted stt_backend_5xx: %+v", e)
		}
	}
	if !g.health.get("small") {
		t.Error("client cancel wrongly demoted the backend (health=false); a cancel is not a backend fault")
	}
	if failures.Len() != 0 {
		t.Errorf("OnRequestFailed must NOT be called on a pure client cancel, got %d call(s): %v", failures.Len(), failures.All())
	}
	if rr.Code != statusClientClosed {
		t.Errorf("status: want %d (client closed), got %d", statusClientClosed, rr.Code)
	}
}

func TestTranscriptionHandler_UpstreamUnreachable_EmitsBackendError(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	failures, restoreFail := withCapturedOnRequestFailed(t)
	defer restoreFail()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: "http://127.0.0.1:1", Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if events.Len() == 0 {
		t.Fatal("no OnError event captured for unreachable backend")
	}
	if events.All()[0].Kind != "stt_backend_error" {
		t.Errorf("kind: want stt_backend_error, got %q", events.All()[0].Kind)
	}
	if failures.Len() == 0 {
		t.Error("OnRequestFailed not called on transport error")
	}
}

func TestTranscriptionHandler_EditIntentPostProcessFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"translate to Russian"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("context-edit output too long")
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true&intent=edit", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var result map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["text"] != "" {
		t.Errorf("text: want empty, got %q (spoken instruction must not be inserted as content)", result["text"])
	}
	if result["mode"] != "edit" {
		t.Errorf("mode: want 'edit', got %q", result["mode"])
	}
	if result["status"] != "failed" {
		t.Errorf("status: want 'failed', got %q", result["status"])
	}
}

func TestTranscriptionHandler_NonEditPostProcessFailureKeepsRawFallback(t *testing.T) {
	signals := captureCleanupSignals(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw transcript"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("cleanup error")
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "raw transcript") {
		t.Errorf("expected raw transcript fallback, got: %s", body2)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(body2), &parsed); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body2)
	}
	if _, has := parsed["status"]; has {
		t.Errorf("status: want none on a cleanup that did not land, got %q", parsed["status"])
	}
	signals.assertSilentSkip(t, EnhanceSkipError)
	if parsed["mode"] != "" {
		t.Errorf("mode: want empty (no post-processing ran), got %q", parsed["mode"])
	}
}
