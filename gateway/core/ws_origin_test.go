package core

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketOriginPatterns(t *testing.T) {
	cases := []struct {
		env  string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"app.example.com", []string{"app.example.com"}},
		{" a.example.com , *.b.example.com:3000 ,, ", []string{"a.example.com", "*.b.example.com:3000"}},
	}
	for _, c := range cases {
		t.Setenv("DICTION_WS_ALLOWED_ORIGINS", c.env)
		got := WebSocketOriginPatterns()
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("env %q: got %q want %q", c.env, got, c.want)
		}
	}
}

// dialWithOrigin dials with the given Origin ("" sends none) and reports a failed upgrade status.
func dialWithOrigin(t *testing.T, origin string) (ok bool, status int) {
	t.Helper()
	srv, _ := startStreamingServer(t, `{"text":"x"}`, http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": []string{origin}}
	}
	conn, resp, err := websocket.Dial(ctx, wsURL(srv, "model=small"), opts)
	if err != nil {
		if resp != nil {
			return false, resp.StatusCode
		}
		return false, 0
	}
	conn.CloseNow()
	return true, http.StatusSwitchingProtocols
}

func TestStreamingWebSocketOriginPolicy(t *testing.T) {
	t.Setenv("DICTION_WS_ALLOWED_ORIGINS", "")
	if ok, st := dialWithOrigin(t, ""); !ok {
		t.Errorf("no Origin header (native client) must be accepted, status %d", st)
	}
	if ok, st := dialWithOrigin(t, "https://evil.example"); ok || st != http.StatusForbidden {
		t.Errorf("foreign browser origin: ok=%v status=%d, want 403", ok, st)
	}
	t.Setenv("DICTION_WS_ALLOWED_ORIGINS", "*.trusted.example, other.example:8443")
	if ok, st := dialWithOrigin(t, "https://app.trusted.example"); !ok {
		t.Errorf("allow-listed origin rejected, status %d", st)
	}
	if ok, st := dialWithOrigin(t, "https://other.example:8443"); !ok {
		t.Errorf("allow-listed origin with port rejected, status %d", st)
	}
	if ok, _ := dialWithOrigin(t, "https://evil.example"); ok {
		t.Error("non-listed origin accepted with an allow-list set")
	}
}

func TestStreamingWebSocketSameHostOriginAccepted(t *testing.T) {
	t.Setenv("DICTION_WS_ALLOWED_ORIGINS", "")
	srv, _ := startStreamingServer(t, `{"text":"x"}`, http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := strings.TrimPrefix(srv.URL, "http://")
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "model=small"),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + host}}})
	if err != nil {
		t.Fatalf("same-host origin rejected: %v", err)
	}
	conn.CloseNow()
}

func TestStreamingForeignOriginIsNotSTTError(t *testing.T) {
	t.Setenv("DICTION_WS_ALLOWED_ORIGINS", "")
	calls, restore := withCapturedOnRequestFailed(t)
	defer restore()
	if ok, st := dialWithOrigin(t, "https://evil.example"); ok || st != http.StatusForbidden {
		t.Fatalf("ok=%v status=%d, want 403", ok, st)
	}
	calls.WaitFor(1, 2*time.Second)
	got := calls.All()
	if len(got) != 1 || got[0] != "ws_origin_rejected" {
		t.Fatalf("OnRequestFailed: want one ws_origin_rejected, got %v", got)
	}
}
