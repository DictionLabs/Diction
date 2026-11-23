package core

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreaming_RelayWinsOverBuiltinPreview(t *testing.T) {
	rl := installFakePreview(t, 0)
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamPCM(t, srv, "preview=cloud", pcmPattern(4000, 1000, 3000), 20*time.Millisecond)

	if n := len(rl.opened()); n != 1 {
		t.Fatalf("relay opened %d times, want 1", n)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("backend saw %d requests, want 1: the built-in preview sent partials", n)
	}
	if got := lastResultText(t, frames); got != "r1" {
		t.Fatalf("final %q, want r1", got)
	}
	if interimCount(t, frames) == 0 {
		t.Fatal("no relay interims")
	}
	for _, f := range frames[:len(frames)-1] {
		if !strings.Contains(f, `"text":"i`) {
			t.Fatalf("frame %q is not a relay interim", f)
		}
	}
}

func TestStreaming_RelayOnly_OneBackendRequest(t *testing.T) {
	rl := installFakePreview(t, 0)
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, LiveConfig{})
	frames := streamPCM(t, srv, "preview=cloud", pcmPattern(4000, 1000, 3000), 20*time.Millisecond)
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("backend saw %d requests, want 1 (the whole clip)", n)
	}
	if got := lastResultText(t, frames); got != "r1" {
		t.Fatalf("final %q, want r1", got)
	}
	if len(rl.opened()) != 1 || interimCount(t, frames) == 0 {
		t.Fatalf("relay opened %d times, %d interims", len(rl.opened()), interimCount(t, frames))
	}
}

func TestStreaming_LiveDecodeRespectsCallerCap(t *testing.T) {
	ogg := testOgg(t, 6)
	prev := StreamPrincipal
	StreamPrincipal = func(_ *http.Request) string { return "caller-live" }
	t.Cleanup(func() { StreamPrincipal = prev })

	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())

	frames := streamOpusFrames(t, srv, "codec=opus&preview=cloud", ogg, true)
	if interimCount(t, frames) == 0 {
		t.Fatalf("no interims with the caller under its cap: %v", frames)
	}
	waitUntil(t, func() bool { return opusPrincipals.Count("caller-live") == 0 }, "caller slot returned")

	for {
		release, ok := opusPrincipals.TryAcquire("caller-live", opusMaxPerPrincipal())
		if !ok {
			break
		}
		t.Cleanup(release)
	}
	held := opusPrincipals.Count("caller-live")
	atomic.StoreInt32(calls, 0)
	frames = streamOpusFrames(t, srv, "codec=opus&preview=cloud", ogg, false)
	if name, _ := lastName.Load().(string); interimCount(t, frames) != 0 || atomic.LoadInt32(calls) != 1 || name != "audio.ogg" {
		t.Fatalf("at the cap: frames %v, calls %d, sent %q; want no live text and the original Ogg", frames, atomic.LoadInt32(calls), name)
	}
	if got := opusPrincipals.Count("caller-live"); got != held {
		t.Fatalf("caller holds %d slots after the session, want %d", got, held)
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
