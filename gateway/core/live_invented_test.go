package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// inventingBackend answers like a backend that echoes its own prompt: far more text than
// a short clip can hold. fail(n) can make call n fail.
func inventingBackend(t *testing.T, fail func(n int) bool) (*httptest.Server, *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(atomic.AddInt32(&n, 1))
		r.ParseMultipartForm(50 << 20) //nolint:errcheck
		if fail != nil && fail(call) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"%s"}`, strings.TrimSpace(strings.Repeat("Alpha Bravo Charlie Delta, ", 20)))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// An accidental short recording answered with invented text: nothing is typed, and the
// same audio is not sent a second time.
func TestStreaming_InventedTextOnShortClipGivesEmptyResult(t *testing.T) {
	backend, calls := inventingBackend(t, nil)
	srv := liveGateway(t, backend.URL, segmentedConfig())
	// Half a second of room noise first, as from a real tap, so the engine hears the
	// tone as sound and transcribes it as the tail.
	frames := streamPCM(t, srv, "", pcmPattern(0, 500, 1500), 0)
	if got := lastResultText(t, frames); got != "" {
		t.Fatalf("final has %d characters from 1.5 s of audio, want none", len(got))
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("backend calls = %d, want 1 (no second request for the same audio)", n)
	}
}

// The whole-clip fallback is held to the same bound as the pieces. Unlike a short clip
// (which may really be silence), audio with speech in it answered with invented text
// is a backend fault: the dictation fails instead of silently typing nothing.
func TestStreaming_InventedTextOnWholeClipFallbackFails(t *testing.T) {
	backend, calls := inventingBackend(t, func(n int) bool { return n == 1 })
	srv := liveGateway(t, backend.URL, segmentedConfig())
	frames := streamPCM(t, srv, "", pcmPattern(4000, 1000, 2000), 0)
	for _, f := range frames {
		if !strings.Contains(f, `"type"`) {
			t.Fatalf("got a result frame %q, want the dictation to fail", f)
		}
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("backend calls = %d, want 2 (failed piece, then the whole clip, no retry)", n)
	}
}

// With pieces off the gateway behaves as upstream: the backend's answer is passed on.
func TestStreaming_InventedTextUntouchedWhenPiecesOff(t *testing.T) {
	backend, _ := inventingBackend(t, nil)
	srv := liveGateway(t, backend.URL, LiveConfig{})
	frames := streamPCM(t, srv, "", pcmPattern(1500), 0)
	if got := lastResultText(t, frames); len(got) < 400 {
		t.Fatalf("final has %d characters, want the backend's answer unchanged", len(got))
	}
}
