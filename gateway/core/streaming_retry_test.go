package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Backend fallback on /v1/audio/stream — parity with the HTTP path's demote-and-retry
// (proxy.go). The audio is already fully buffered when the backend call fails, so a
// retry costs one more POST. Plan: .claude/plans/stream-fallback-cleanup-signal-plan.md.

// countingBackend answers every request with status (and text when 200) and counts hits.
func countingBackend(t *testing.T, status int, text string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			fmt.Fprintf(w, `{"text":%q}`, text)
		} else {
			fmt.Fprint(w, `{"error":"backend says no"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// twoBackendStreamServer wires primary as the default model and fallback as the
// routing fallback, both healthy — the same shape the HTTP retry tests use.
func twoBackendStreamServer(t *testing.T, primary, fallback Backend) (*httptest.Server, *Gateway) {
	t.Helper()
	g := &Gateway{
		backends:      []Backend{primary, fallback},
		health:        newHealthState(),
		defaultModel:  primary.Name,
		fallbackModel: fallback.Name,
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set(primary.Name, true)
	g.health.set(fallback.Name, true)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)
	return srv, g
}

// streamPCMAndRead sends silence + done and returns the result frame, or the read error.
func streamPCMAndRead(t *testing.T, srv *httptest.Server) (map[string]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=en"), nil)
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
		return nil, err
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, data)
	}
	return result, nil
}

// capturedEvents is a race-safe OnError sink: the handler goroutine writes, the test reads.
type capturedEvents struct {
	mu     sync.Mutex
	events []ErrorEvent
}

func (c *capturedEvents) snapshot() []ErrorEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ErrorEvent(nil), c.events...)
}

func captureStreamHooks(t *testing.T) (*capturedEvents, *atomic.Int32) {
	t.Helper()
	ev := &capturedEvents{}
	var failed atomic.Int32
	prevErr, prevFailed := OnError, OnRequestFailed
	OnError = func(_ context.Context, e ErrorEvent) {
		ev.mu.Lock()
		ev.events = append(ev.events, e)
		ev.mu.Unlock()
	}
	OnRequestFailed = func(_ context.Context, _ string) { failed.Add(1) }
	t.Cleanup(func() { OnError, OnRequestFailed = prevErr, prevFailed })
	return ev, &failed
}

func TestStreamRetry_Primary5xx_FallbackRescuesTheDictation(t *testing.T) {
	ev, failed := captureStreamHooks(t)
	primary, primaryHits := countingBackend(t, http.StatusInternalServerError, "")
	fallback, fallbackHits := countingBackend(t, http.StatusOK, "rescued text")
	srv, g := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})

	result, err := streamPCMAndRead(t, srv)
	if err != nil {
		t.Fatalf("want a transcript after the retry, socket closed: %v", err)
	}
	if result["text"] != "rescued text" {
		t.Errorf("text: want the fallback's transcript, got %q", result["text"])
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 1 {
		t.Errorf("hits: primary=%d fallback=%d, want 1/1", primaryHits.Load(), fallbackHits.Load())
	}
	if g.health.get("primary") {
		t.Error("primary still healthy after a 5xx")
	}
	if failed.Load() != 0 {
		t.Errorf("OnRequestFailed fired %d times for a rescued dictation", failed.Load())
	}
	evs := ev.snapshot()
	if len(evs) != 1 || evs[0].Kind != kindSTTBackend5xx || !strings.Contains(evs[0].Hint, "retrying on fallback") {
		t.Errorf("want one stt_backend_5xx event naming the retry, got %+v", evs)
	}
}

func TestStreamRetry_PrimaryUnreachable_FallbackRescuesTheDictation(t *testing.T) {
	_, failed := captureStreamHooks(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // connection refused — the 2026-08-26 redeploy shape
	fallback, fallbackHits := countingBackend(t, http.StatusOK, "rescued text")
	srv, g := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: deadURL, Aliases: []string{"primary"}},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})

	result, err := streamPCMAndRead(t, srv)
	if err != nil {
		t.Fatalf("want a transcript after the retry, socket closed: %v", err)
	}
	if result["text"] != "rescued text" || fallbackHits.Load() != 1 {
		t.Errorf("text=%q fallbackHits=%d", result["text"], fallbackHits.Load())
	}
	if g.health.get("primary") {
		t.Error("an unreachable primary must be demoted so re-selection can pick something else")
	}
	if failed.Load() != 0 {
		t.Errorf("OnRequestFailed fired %d times for a rescued dictation", failed.Load())
	}
}

func TestStreamRetry_BothFail_FailsOnceWithTwoEvents(t *testing.T) {
	ev, failed := captureStreamHooks(t)
	primary, _ := countingBackend(t, http.StatusInternalServerError, "")
	fallback, fallbackHits := countingBackend(t, http.StatusBadGateway, "")
	srv, g := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})

	if _, err := streamPCMAndRead(t, srv); err == nil {
		t.Fatal("expected the socket to close when both backends fail")
	}
	waitFor(t, func() bool { return failed.Load() == 1 })
	if fallbackHits.Load() != 1 {
		t.Errorf("fallback hits: want 1, got %d", fallbackHits.Load())
	}
	evs := ev.snapshot()
	if len(evs) != 2 || evs[1].Provider != "fallback" || evs[1].HTTPStatus != http.StatusBadGateway {
		t.Errorf("want primary + retry events, second naming the fallback with 502, got %+v", evs)
	}
	if g.health.get("fallback") {
		t.Error("a retry backend that also 5xxes must be demoted too")
	}
}

func TestStreamRetry_NotRetried_On4xxOrHallucination(t *testing.T) {
	cases := []struct {
		name   string
		status int
		text   string
	}{
		{"4xx is our request, not a sick backend", http.StatusUnprocessableEntity, ""},
		{"hallucination depends on the audio", http.StatusOK, strings.Repeat("the the ", 40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, failed := captureStreamHooks(t)
			primary, _ := countingBackend(t, tc.status, tc.text)
			fallback, fallbackHits := countingBackend(t, http.StatusOK, "should not be used")
			srv, _ := twoBackendStreamServer(t,
				Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
				Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})

			if _, err := streamPCMAndRead(t, srv); err == nil {
				t.Fatal("expected failure")
			}
			waitFor(t, func() bool { return failed.Load() == 1 })
			if fallbackHits.Load() != 0 {
				t.Errorf("fallback was called %d times; this failure must not retry", fallbackHits.Load())
			}
		})
	}
}

// Passthrough primary (raw Ogg forwarded) failing over to a NeedsWAV backend: the
// buffered container must be converted, or the retry backend gets bytes it cannot read.
func TestStreamRetry_PassthroughPrimary_ConvertsForNeedsWAVFallback(t *testing.T) {
	ogg := genOggOpus(t)
	primary, _ := countingBackend(t, http.StatusInternalServerError, "")
	cb := newCaptureBackend(t, "converted")
	srv, _ := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}, NeedsWAV: false},
		Backend{Name: "fallback", URL: cb.srv.URL, Aliases: []string{"fallback"}, NeedsWAV: true})

	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != "converted" {
		t.Errorf("text: want 'converted', got %q", text)
	}
	cb.mu.Lock()
	body := cb.body
	cb.mu.Unlock()
	if !bytes.HasPrefix(body, []byte("RIFF")) {
		t.Errorf("NeedsWAV fallback received %d bytes starting %q, want a WAV", len(body), body[:min(4, len(body))])
	}
}

// A single-backend (typical self-hosted) gateway has nowhere to retry. A transport
// fault there must not take its only backend out of rotation for 120 s — before the
// retry existed the socket path never demoted on transport faults, and demoting buys
// nothing when there is no alternative to route to. A 5xx keeps demoting (f9bb47ee).
func TestStreamRetry_NoAlternative_TransportFaultKeepsTheOnlyBackend(t *testing.T) {
	_, failed := captureStreamHooks(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	g := &Gateway{
		backends:     []Backend{{Name: "only", URL: deadURL, Aliases: []string{"only"}}},
		health:       newHealthState(),
		defaultModel: "only",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("only", true)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	if _, err := streamPCMAndRead(t, srv); err == nil {
		t.Fatal("expected failure")
	}
	waitFor(t, func() bool { return failed.Load() == 1 })
	if !g.health.get("only") {
		t.Error("the only backend was demoted by a transport fault with no alternative to route to")
	}
}

func TestIsRetryableBackendFailure(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("backend returned 500: boom"), true},
		{errors.New("backend returned 503: warming"), true},
		{fmt.Errorf("backend request: %w", errors.New("dial tcp: connection refused")), true},
		{errors.New("backend returned 422: bad"), false},
		{errSTTHallucination, false},
		{fmt.Errorf("backend request: %w", context.Canceled), false},
		{fmt.Errorf("backend request: %w", context.DeadlineExceeded), false},
		{errors.New("decode response: unexpected EOF"), false},
		{errors.New("create form file: nope"), false},
	}
	for _, tc := range cases {
		if got := isRetryableBackendFailure(tc.err); got != tc.want {
			t.Errorf("isRetryableBackendFailure(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
