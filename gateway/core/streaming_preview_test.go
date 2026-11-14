package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakePreview is a PreviewSession that spams interims ("i1", "i2", …) from its own
// goroutine until stopped — the worst case for ordering against the result frame.
type fakePreview struct {
	ch       chan string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	sent     int
	dieAfter int // >0: end the relay on its own after this many interims
}

func newFakePreview(dieAfter int) *fakePreview {
	f := &fakePreview{ch: make(chan string), stop: make(chan struct{}), done: make(chan struct{}), dieAfter: dieAfter}
	go func() {
		defer close(f.done)
		defer close(f.ch)
		for i := 1; ; i++ {
			if f.dieAfter > 0 && i > f.dieAfter {
				return
			}
			select {
			case f.ch <- fmt.Sprintf("i%d", i):
			case <-f.stop:
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return f
}

func (f *fakePreview) Send([]byte) {
	f.mu.Lock()
	f.sent++
	f.mu.Unlock()
}
func (f *fakePreview) Interims() <-chan string { return f.ch }
func (f *fakePreview) Stop() {
	f.once.Do(func() { close(f.stop) })
	<-f.done
}

// fakeRelayLog records what the handler did with the preview hooks.
type fakeRelayLog struct {
	mu       sync.Mutex
	sessions []*fakePreview
	outcomes []string
}

func (l *fakeRelayLog) opened() []*fakePreview {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*fakePreview(nil), l.sessions...)
}

func (l *fakeRelayLog) reported() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.outcomes...)
}

func (f *fakePreview) frames() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent
}

// installFakePreview sets PreviewRelay/OnPreviewRelay for one test.
func installFakePreview(t *testing.T, dieAfter int) *fakeRelayLog {
	t.Helper()
	l := &fakeRelayLog{}
	prevRelay, prevHook := PreviewRelay, OnPreviewRelay
	PreviewRelay = func(ctx context.Context, language string) PreviewSession {
		f := newFakePreview(dieAfter)
		l.mu.Lock()
		l.sessions = append(l.sessions, f)
		l.mu.Unlock()
		return f
	}
	OnPreviewRelay = func(ctx context.Context, outcome string) {
		l.mu.Lock()
		l.outcomes = append(l.outcomes, outcome)
		l.mu.Unlock()
	}
	t.Cleanup(func() { PreviewRelay, OnPreviewRelay = prevRelay, prevHook })
	return l
}

// runPreviewSession sends `frames` PCM frames, waits, sends done, and returns every
// text frame the server wrote, in order.
func runPreviewSession(t *testing.T, srv *httptest.Server, query string, frames int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, query), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	var got []string
	var mu sync.Mutex
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				mu.Lock()
				got = append(got, string(data))
				mu.Unlock()
			}
		}
	}()
	for i := 0; i < frames; i++ {
		if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
			t.Fatalf("write audio: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}
	<-readDone
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

func isInterim(frame string) bool {
	var f struct {
		Type string `json:"type"`
	}
	return json.Unmarshal([]byte(frame), &f) == nil && f.Type == "interim"
}

func TestStreamingPreview_InterimsThenResult_NeverAfter(t *testing.T) {
	rl := installFakePreview(t, 0)
	srv, _ := startStreamingServer(t, "final words", http.StatusOK)

	for run := 0; run < 5; run++ {
		frames := runPreviewSession(t, srv, "language=cs&preview=cloud", 5)
		if len(frames) < 2 {
			t.Fatalf("want interims + result, got %q", frames)
		}
		last := frames[len(frames)-1]
		if last != `{"text":"final words"}` {
			t.Fatalf("last frame %q, want the result", last)
		}
		for _, f := range frames[:len(frames)-1] {
			if !isInterim(f) {
				t.Fatalf("unexpected non-interim before the result: %q", f)
			}
		}
	}
	if n := len(rl.opened()); n != 5 {
		t.Fatalf("relay opened %d times, want once per session", n)
	}
	if n := rl.opened()[0].frames(); n != 5 {
		t.Errorf("relay got %d frames, want 5", n)
	}
}

// Golden: a session without `preview` is byte-identical to the pre-change handler,
// and the relay is never opened even when the hook is installed.
func TestStreamingPreview_NoPreviewParam_ByteIdentical(t *testing.T) {
	srv, _ := startStreamingServer(t, "final words", http.StatusOK)
	before := runPreviewSession(t, srv, "language=cs", 3)

	rl := installFakePreview(t, 0)
	after := runPreviewSession(t, srv, "language=cs", 3)
	other := runPreviewSession(t, srv, "language=cs&preview=local", 3)

	want := []string{`{"text":"final words"}`}
	for name, got := range map[string][]string{"before": before, "after": after, "preview=local": other} {
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("%s: frames %q, want %q", name, got, want)
		}
	}
	if len(rl.opened()) != 0 || len(rl.reported()) != 0 {
		t.Fatalf("relay touched without preview=cloud: %d sessions, outcomes %q", len(rl.opened()), rl.reported())
	}
}

// A relay that dies mid-session leaves the final untouched.
func TestStreamingPreview_RelayEndsEarly_FinalUnchanged(t *testing.T) {
	installFakePreview(t, 2)
	srv, _ := startStreamingServer(t, "final words", http.StatusOK)
	frames := runPreviewSession(t, srv, "language=cs&preview=cloud", 5)
	if frames[len(frames)-1] != `{"text":"final words"}` {
		t.Fatalf("frames %q", frames)
	}
	if n := len(frames) - 1; n > 2 {
		t.Fatalf("%d interims from a relay that sent 2", n)
	}
}

// Community build: hook nil → preview=cloud is ignored.
func TestStreamingPreview_NoHook_Ignored(t *testing.T) {
	prev := PreviewRelay
	PreviewRelay = nil
	t.Cleanup(func() { PreviewRelay = prev })
	srv, _ := startStreamingServer(t, "final words", http.StatusOK)
	frames := runPreviewSession(t, srv, "language=cs&preview=cloud", 3)
	if len(frames) != 1 || frames[0] != `{"text":"final words"}` {
		t.Fatalf("frames %q", frames)
	}
}

// Opus has no PCM while recording: no relay, outcome failed.
func TestStreamingPreview_Opus_NoRelay(t *testing.T) {
	rl := installFakePreview(t, 0)
	g := makeGateway(echoTranscript(t, "x").URL, false, 10<<20) // passthrough
	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus&preview=cloud"), &websocket.DialOptions{Subprotocols: []string{opusSubprotocol}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break
		}
	}
	if len(rl.opened()) != 0 {
		t.Fatal("relay opened for an Opus session")
	}
	if outs := rl.reported(); len(outs) != 1 || outs[0] != "failed" {
		t.Fatalf("outcomes %q, want [failed]", outs)
	}
}

// A client that walks away mid-recording still gets its relay stopped (deferred stop).
func TestStreamingPreview_ClientAbort_StopsRelay(t *testing.T) {
	rl := installFakePreview(t, 0)
	srv, _ := startStreamingServer(t, "final words", http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "preview=cloud"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
	time.Sleep(30 * time.Millisecond)
	conn.CloseNow() // client walks away mid-recording
	deadline := time.Now().Add(2 * time.Second)
	for {
		if opened := rl.opened(); len(opened) == 1 {
			select {
			case <-opened[0].done:
				return // relay stopped by the deferred stop on the early return
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("relay not stopped after the client went away")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
