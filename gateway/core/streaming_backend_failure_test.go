package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DictionLabs/Diction/gateway/core/transcript"

	"github.com/coder/websocket"
)

func TestClassifyBackendFailure_MatchesHTTPPathVocabulary(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantKind   string
		wantStatus int
	}{
		{"hallucination", transcript.ErrSTTHallucination, kindSTTHallucination, 0},
		{
			"client cancel is not a backend fault",
			fmt.Errorf("backend request: %w", context.Canceled),
			kindSTTUpstreamCanceled, 0,
		},
		{
			"deadline is the backend stalling, not a long dictation",
			fmt.Errorf("backend request: %w", context.DeadlineExceeded),
			kindSTTBackendError, 0,
		},
		{"500 demotes", errors.New("backend returned 500: internal error"), kindSTTBackend5xx, 500},
		{"503 demotes", errors.New("backend returned 503: unavailable"), kindSTTBackend5xx, 503},
		{"422 does not demote", errors.New("backend returned 422: bad audio"), kindSTTBackendError, 422},
		{"400 does not demote", errors.New("backend returned 400: no model"), kindSTTBackendError, 400},
		{"decode", errors.New("decode response: invalid character"), kindSTTBackendError, 0},
		{"transport", errors.New("backend request: dial tcp: refused"), kindSTTBackendError, 0},
		{"unknown", errors.New("something else entirely"), kindSTTBackendError, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, hint, status := classifyBackendFailure(tc.err)
			if kind != tc.wantKind {
				t.Errorf("kind: want %s, got %s", tc.wantKind, kind)
			}
			if status != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, status)
			}
			if hint == "" {
				t.Error("hint must never be empty — it is the only human-readable field")
			}
		})
	}
}

func TestClassifyBackendFailure_NeverLeaksTheResponseBody(t *testing.T) {
	err := errors.New("backend returned 500: {\"text\":\"Endre said hello world\"}")
	kind, hint, status := classifyBackendFailure(err)
	if kind != kindSTTBackend5xx || status != 500 {
		t.Fatalf("want stt_backend_5xx/500, got %s/%d", kind, status)
	}
	for _, needle := range []string{"Endre", "hello world", "text"} {
		if strings.Contains(hint, needle) {
			t.Errorf("hint leaked transcript content %q: %q", needle, hint)
		}
	}
}

func TestBackendStatusFromErr(t *testing.T) {
	cases := map[string]int{
		"backend returned 500: boom":        500,
		"backend returned 404: nope":        404,
		"wrapped: backend returned 502: hi": 502,
		"backend returned 5: truncated":     0,
		"backend returned 50000: too big":   0,
		"backend returned abc: letters":     0,
		"backend returned ":                 0,
		"no marker here":                    0,
		"":                                  0,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := backendStatusFromErr(in); got != want {
				t.Errorf("backendStatusFromErr(%q): want %d, got %d", in, want, got)
			}
		})
	}
}

func TestStreamingHandler_Backend5xx_EmitsStatusAndDemotes(t *testing.T) {
	events, restore := withCapturedOnError(t)
	defer restore()

	srv, g := startStreamingServer(t, "", http.StatusInternalServerError)

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

	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("expected connection closed after backend 5xx")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Len() == 0 {
		t.Fatal("no OnError event captured")
	}
	got := events.All()[0]
	if got.Kind != kindSTTBackend5xx {
		t.Errorf("kind: want %s, got %q", kindSTTBackend5xx, got.Kind)
	}
	if got.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("http_status: want 500, got %d — the status is the whole point", got.HTTPStatus)
	}
	if got.Endpoint != "/v1/audio/stream" {
		t.Errorf("endpoint: want /v1/audio/stream, got %q", got.Endpoint)
	}
	if g.health.get("small") {
		t.Error("backend still healthy after a 5xx — it must be demoted so the next dictation routes elsewhere")
	}
}

func TestStreamingHandler_Backend4xx_KeepsBackendHealthy(t *testing.T) {
	events, restore := withCapturedOnError(t)
	defer restore()

	srv, g := startStreamingServer(t, "", http.StatusUnprocessableEntity)

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

	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("expected connection closed after backend 4xx")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Len() == 0 {
		t.Fatal("no OnError event captured")
	}
	got := events.All()[0]
	if got.Kind != kindSTTBackendError {
		t.Errorf("kind: want %s, got %q", kindSTTBackendError, got.Kind)
	}
	if got.HTTPStatus != http.StatusUnprocessableEntity {
		t.Errorf("http_status: want 422, got %d", got.HTTPStatus)
	}
	if !g.health.get("small") {
		t.Error("backend demoted on a 4xx — a bad request must not take the backend down for everyone")
	}
}

func sendAndReceiveStreamResult(ctx context.Context, t *testing.T, wsAddr string) map[string]string {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsAddr, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
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
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, data)
	}
	return result
}

func TestStreamingHandler_EditIntentPostProcessFailure(t *testing.T) {
	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("llm output too long")
	}
	srv := startStreamingServerWithPostProcess(t, "translate to Russian", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := sendAndReceiveStreamResult(ctx, t, wsURL(srv, "model=small&enhance=true&intent=edit"))

	if result["text"] != "" {
		t.Errorf("text: want empty, got %q (instruction must not be inserted as content)", result["text"])
	}
	if result["mode"] != "edit" {
		t.Errorf("mode: want 'edit', got %q", result["mode"])
	}
	if result["status"] != "failed" {
		t.Errorf("status: want 'failed', got %q", result["status"])
	}
}

func TestStreamingHandler_EditSelectedIntentPostProcessFailure(t *testing.T) {
	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("llm error")
	}
	srv := startStreamingServerWithPostProcess(t, "translate this to Russian please", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := sendAndReceiveStreamResult(ctx, t, wsURL(srv, "model=small&enhance=true&intent=edit-selected"))

	if result["text"] != "" {
		t.Errorf("text: want empty, got %q", result["text"])
	}
	if result["mode"] != "edit-selected" {
		t.Errorf("mode: want 'edit-selected', got %q", result["mode"])
	}
	if result["status"] != "failed" {
		t.Errorf("status: want 'failed', got %q", result["status"])
	}
}

func TestStreamingHandler_NonEditPostProcessFailureKeepsRawFallback(t *testing.T) {
	signals := captureCleanupSignals(t)
	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("cleanup error")
	}
	srv := startStreamingServerWithPostProcess(t, "raw transcript text", postProcess)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := sendAndReceiveStreamResult(ctx, t, wsURL(srv, "model=small&enhance=true"))

	if result["text"] != "raw transcript text" {
		t.Errorf("text: want 'raw transcript text', got %q", result["text"])
	}
	if _, has := result["status"]; has {
		t.Errorf("status: want none on a cleanup that did not land, got %q", result["status"])
	}
	signals.assertSilentSkip(t, EnhanceSkipError)
}

func TestStreamingHandler_NoAudio_EmitsEventWithSocketMetrics(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	calls, restoreFailed := withCapturedOnRequestFailed(t)
	defer restoreFailed()

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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Len() != 1 {
		t.Fatalf("OnError calls: want 1, got %d: %+v", events.Len(), events.All())
	}
	ev := events.All()[0]
	if ev.Kind != "ws_no_audio" {
		t.Errorf("Kind: want ws_no_audio, got %q", ev.Kind)
	}
	if ev.Source != "streaming" {
		t.Errorf("Source: want streaming, got %q", ev.Source)
	}
	if ev.InputChars != 0 {
		t.Errorf("InputChars (frame count): want 0, got %d", ev.InputChars)
	}
	if ev.LatencyMs < 0 {
		t.Errorf("LatencyMs (socket-open ms): want >= 0, got %d", ev.LatencyMs)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Len() != 1 {
		t.Fatalf("OnRequestFailed calls: want 1, got %d: %v", calls.Len(), calls.All())
	}
	if calls.All()[0] != errTypeNoAudio {
		t.Errorf("errorType: want %q, got %q", errTypeNoAudio, calls.All()[0])
	}
}

func TestStreamingHandler_PreUpgrade503_EmitsRouteFailAndSetsHeader(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	calls, restoreFailed := withCapturedOnRequestFailed(t)
	defer restoreFailed()

	srv, g := startStreamingServer(t, "ok", http.StatusOK)
	g.health.set("small", false)

	resp, err := http.Get(srv.URL + "/v1/audio/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Diction-Route-Model"); got != "small" {
		t.Errorf("X-Diction-Route-Model: want small, got %q", got)
	}

	if events.Len() != 1 {
		t.Fatalf("OnError calls: want 1, got %d: %+v", events.Len(), events.All())
	}
	if events.All()[0].Kind != "stt_route_fail" {
		t.Errorf("Kind: want stt_route_fail, got %q", events.All()[0].Kind)
	}
	if events.All()[0].HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus: want 503, got %d", events.All()[0].HTTPStatus)
	}

	if calls.Len() != 1 {
		t.Fatalf("OnRequestFailed calls: want 1, got %d: %v", calls.Len(), calls.All())
	}
	if calls.All()[0] != errTypeSTTError {
		t.Errorf("errorType: want %q, got %q", errTypeSTTError, calls.All()[0])
	}
}

func TestStreamingHandler_ClientGoneAfterSuccess_NotCountedAsFailure(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	calls, restoreFailed := withCapturedOnRequestFailed(t)
	defer restoreFailed()

	srv, _ := startStreamingServer(t, "transcribed", http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	_ = conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
	conn.CloseNow()

	time.Sleep(200 * time.Millisecond)

	for _, c := range calls.All() {
		if c == errTypeSTTError {
			t.Errorf("OnRequestFailed(%q) fired after a successful transcription — a client that walked away must not inflate the failure rate: %v", errTypeSTTError, calls.All())
		}
	}
	for _, ev := range events.All() {
		if ev.Kind == "ws_write" && ev.Hint != "client gone before result write" {
			t.Errorf("unexpected ws_write hint: %q", ev.Hint)
		}
	}
}
