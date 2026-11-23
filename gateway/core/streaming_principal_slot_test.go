package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testSetOpusSem(t *testing.T, n int) {
	t.Helper()
	getOpusSem()
	prev := opusSemCh
	opusSemCh = make(chan struct{}, n)
	t.Cleanup(func() { opusSemCh = prev })
}

func newTestServerFor(t *testing.T, g *Gateway) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	t.Cleanup(srv.Close)
	return srv
}

func dialOpus(ctx context.Context, t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{opusSubprotocol}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func TestOpusSlot_IdleSocketDoesNotHoldSlot(t *testing.T) {
	ogg := genOggOpus(t)
	testSetOpusSem(t, 1)
	backend := echoTranscript(t, "hello")
	srv := newTestServerFor(t, makeGateway(backend.URL, true, 10<<20))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	squatter := dialOpus(ctx, t, wsURL(srv, "codec=opus"))
	if err := squatter.Write(ctx, websocket.MessageText, []byte(`{"app":"x"}`)); err != nil {
		t.Fatalf("squatter write: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("real dictation failed while an idle socket was open: %v (after %v)", err, time.Since(start))
	}
	if text != "hello" {
		t.Errorf("transcript = %q, want hello", text)
	}
}

func TestStreamIdle_TextFramesDoNotResetIdle(t *testing.T) {
	backend := echoTranscript(t, "hello")
	g := makeGateway(backend.URL, false, 10<<20)
	g.streamIdleTimeout = 300 * time.Millisecond
	srv := newTestServerFor(t, g)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, ""), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	closed := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(ctx)
		closed <- err
	}()

	ping, _ := json.Marshal(map[string]string{"action": "keepalive"})
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case err := <-closed:
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("want idle_timeout close (1008), got %v", err)
			}
			return
		case <-tick.C:
			_ = conn.Write(ctx, websocket.MessageText, ping)
		case <-deadline:
			t.Fatal("socket sending only text frames was never idle-closed")
		}
	}
}

func TestStreamIdle_AudioFramesResetIdle(t *testing.T) {
	backend := echoTranscript(t, "hello")
	g := makeGateway(backend.URL, false, 10<<20)
	g.streamIdleTimeout = 300 * time.Millisecond
	srv := newTestServerFor(t, g)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, ""), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	for i := 0; i < 8; i++ {
		if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 640)); err != nil {
			t.Fatalf("audio write %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, data, err := conn.Read(ctx); err != nil {
		t.Fatalf("read result: %v", err)
	} else if !json.Valid(data) {
		t.Fatalf("result not JSON: %s", data)
	}
}

func TestOpusSlot_PerPrincipalCap(t *testing.T) {
	ogg := genOggOpus(t)
	testSetOpusSem(t, 8)
	t.Setenv("DICTION_OPUS_MAX_PER_PRINCIPAL", "1")
	prev := StreamPrincipal
	StreamPrincipal = func(r *http.Request) string { return r.Header.Get("X-Test-Principal") }
	t.Cleanup(func() { StreamPrincipal = prev })

	backend := echoTranscript(t, "hello")
	srv := newTestServerFor(t, makeGateway(backend.URL, true, 10<<20))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dial := func(principal string) *websocket.Conn {
		h := http.Header{}
		h.Set("X-Test-Principal", principal)
		conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"),
			&websocket.DialOptions{Subprotocols: []string{opusSubprotocol}, HTTPHeader: h})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}

	first := dial("A")
	if err := first.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	second := dial("A")
	if err := second.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	start := time.Now()
	assertCloseCode(t, second, wsCloseFailed)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("over-cap refusal took %v; want immediate", d)
	}

	third := dial("B")
	if err := third.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		t.Fatalf("B write: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = third.Write(ctx, websocket.MessageText, done)
	_, data, err := third.Read(ctx)
	if err != nil {
		t.Fatalf("principal B refused: %v", err)
	}
	var res struct{ Text string }
	_ = json.Unmarshal(data, &res)
	if res.Text != "hello" {
		t.Errorf("B transcript = %q", res.Text)
	}

	_ = first.Write(ctx, websocket.MessageText, done)
	if _, _, err := first.Read(ctx); err != nil {
		t.Fatalf("first dictation: %v", err)
	}
	first.Close(websocket.StatusNormalClosure, "")
	time.Sleep(150 * time.Millisecond)
	if got := opusPrincipals.Count("A"); got != 0 {
		t.Errorf("principal A still holds %d slots after finishing", got)
	}
}

func TestOpusSlot_ReleasedAfterDecodeNotAfterSTT(t *testing.T) {
	ogg := genOggOpus(t)
	testSetOpusSem(t, 8)
	t.Setenv("DICTION_OPUS_MAX_PER_PRINCIPAL", "1")
	prev := StreamPrincipal
	StreamPrincipal = func(r *http.Request) string { return r.Header.Get("X-Test-Principal") }
	t.Cleanup(func() { StreamPrincipal = prev })

	entered := make(chan struct{})
	unblock := make(chan struct{})
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-unblock
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})
	srv := newTestServerFor(t, makeGateway(backend.URL, true, 10<<20))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		h := http.Header{}
		h.Set("X-Test-Principal", "A")
		conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"),
			&websocket.DialOptions{Subprotocols: []string{opusSubprotocol}, HTTPHeader: h})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})

	first := dial()
	if err := first.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := first.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("first done: %v", err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first dictation never reached STT")
	}

	second := dial()
	if err := second.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if err := second.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("second done: %v", err)
	}
	_, data, err := second.Read(ctx)
	if err != nil {
		t.Fatalf("second dictation refused while the first sat in STT: %v", err)
	}
	var res struct{ Text string }
	_ = json.Unmarshal(data, &res)
	if res.Text != "hello" {
		t.Errorf("second transcript = %q", res.Text)
	}

	close(unblock)
	if _, _, err := first.Read(ctx); err != nil {
		t.Fatalf("first dictation: %v", err)
	}
	first.Close(websocket.StatusNormalClosure, "")
	second.Close(websocket.StatusNormalClosure, "")
	time.Sleep(150 * time.Millisecond)
	if got := opusPrincipals.Count("A"); got != 0 {
		t.Errorf("principal A still holds %d slots (double release or leak)", got)
	}
}
