package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStreamingHandler_EmitsReasonOnWSRead_GoingAway(t *testing.T) {
	events, restore := withCapturedOnError(t)
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
	for time.Now().Before(deadline) {
		if events.Len() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if events.Len() == 0 {
		t.Fatal("no OnError event captured")
	}
	got := events.All()[0]
	if got.Kind != "ws_read" {
		t.Errorf("kind: want ws_read, got %q", got.Kind)
	}
	if got.Reason != wsReasonGoingAway {
		t.Errorf("reason: want %q, got %q", wsReasonGoingAway, got.Reason)
	}
}

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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Len() == 0 {
		t.Fatal("no OnError emitted for idle timeout")
	}
	if events.All()[0].Reason != wsReasonIdleTimeout {
		t.Errorf("emitted reason: want %q, got %q", wsReasonIdleTimeout, events.All()[0].Reason)
	}
}

func TestStreamingHandler_IdleTimeout_HappyPathUnaffected(t *testing.T) {
	srv, g := startStreamingServer(t, "alive", http.StatusOK)
	g.streamIdleTimeout = 2 * time.Second

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

func TestStreamingHandler_WSReadError_TriggersOnRequestFailed(t *testing.T) {
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
	for time.Now().Before(deadline) && calls.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Len() == 0 {
		t.Fatal("OnRequestFailed not called on ws_read error")
	}
	if calls.All()[0] != errTypeClientAbort {
		t.Errorf("errorType: want %q, got %q", errTypeClientAbort, calls.All()[0])
	}
}

func TestStreamingHandler_IdleTimeout_IsClientAbort(t *testing.T) {
	calls, restore := withCapturedOnRequestFailed(t)
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
	_, _, _ = conn.Read(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Len() == 0 {
		t.Fatal("OnRequestFailed not called on idle timeout")
	}
	if calls.All()[0] != errTypeClientAbort {
		t.Errorf("errorType: want %q, got %q", errTypeClientAbort, calls.All()[0])
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
	_, _, _ = conn.Read(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Len() == 0 {
		t.Fatal("OnRequestFailed not called for no-audio path")
	}
	if calls.All()[0] != errTypeNoAudio {
		t.Errorf("errorType: want %q, got %q", errTypeNoAudio, calls.All()[0])
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
	_, _, _ = conn.Read(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Len() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Len() == 0 {
		t.Fatal("OnRequestFailed not called on backend failure")
	}
	if calls.All()[0] != errTypeSTTError {
		t.Errorf("errorType: want %q, got %q", errTypeSTTError, calls.All()[0])
	}
}

func TestStreamingHandler_HappyPath_NoRequestFailed(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)
	if calls.Len() != 0 {
		t.Errorf("OnRequestFailed fired on happy path: %v", calls.All())
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
		{"abnormal closure from iOS", errors.New("failed to get reader: failed to handle control frame opClose: received invalid close payload: invalid status code StatusAbnormalClosure"), wsReasonAbnormalClose},
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
