package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Race-free capture of the package-global hooks (OnError, OnRequestFailed).
//
// Two races existed under -race (2026-11-09): the capture closure appended to a
// plain slice from the handler goroutine while the test read it, and a test
// swapped the global hook while a previous test's hijacked-WebSocket handler was
// still running (httptest.Server.Close does not wait for hijacked conns). The
// recorder guards its slice with a mutex, and every streaming handler under test
// is wrapped in trackHandler so installing or restoring a hook first waits for
// all of them to finish.

// activeHandlers counts streaming handlers still running in this test binary.
var activeHandlers sync.WaitGroup

// trackHandler wraps a handler so waitHandlers can wait for it to return.
func trackHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeHandlers.Add(1)
		defer activeHandlers.Done()
		h.ServeHTTP(w, r)
	})
}

// waitHandlers blocks until every tracked handler has returned (10 s cap, so a
// stuck handler fails loudly instead of hanging the suite).
func waitHandlers(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { activeHandlers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Errorf("streaming handlers still running after 10s")
	}
}

// hookRecorder is a mutex-guarded slice of hook calls.
type hookRecorder[T any] struct {
	mu    sync.Mutex
	items []T
	added chan struct{}
}

func newHookRecorder[T any]() *hookRecorder[T] {
	return &hookRecorder[T]{added: make(chan struct{}, 1)}
}

func (r *hookRecorder[T]) add(v T) {
	r.mu.Lock()
	r.items = append(r.items, v)
	r.mu.Unlock()
	select {
	case r.added <- struct{}{}:
	default:
	}
}

// All returns a copy of the recorded calls.
func (r *hookRecorder[T]) All() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.items...)
}

// Len returns the number of recorded calls.
func (r *hookRecorder[T]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// WaitFor blocks until at least n calls are recorded or timeout passes.
func (r *hookRecorder[T]) WaitFor(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for r.Len() < n {
		select {
		case <-r.added:
		case <-deadline:
			return r.Len() >= n
		}
	}
	return true
}

// withCapturedOnError installs a temporary core.OnError recording every event.
// The returned restore func (also registered with t.Cleanup, idempotent) waits
// for running handlers, then puts the prior hook back. Not parallel-safe.
func withCapturedOnError(t *testing.T) (*hookRecorder[ErrorEvent], func()) {
	t.Helper()
	waitHandlers(t)
	rec := newHookRecorder[ErrorEvent]()
	prev := OnError
	OnError = func(_ context.Context, e ErrorEvent) { rec.add(e) }
	var once sync.Once
	restore := func() { once.Do(func() { waitHandlers(t); OnError = prev }) }
	t.Cleanup(restore)
	return rec, restore
}

// withCapturedOnRequestFailed installs a temporary core.OnRequestFailed
// recording every errorType. Same restore contract as withCapturedOnError.
func withCapturedOnRequestFailed(t *testing.T) (*hookRecorder[string], func()) {
	t.Helper()
	waitHandlers(t)
	rec := newHookRecorder[string]()
	prev := OnRequestFailed
	OnRequestFailed = func(_ context.Context, errorType string) { rec.add(errorType) }
	var once sync.Once
	restore := func() { once.Do(func() { waitHandlers(t); OnRequestFailed = prev }) }
	t.Cleanup(restore)
	return rec, restore
}

// Every hook-capture helper must wait for still-running tracked handlers before
// swapping the global hooks: a streaming handler emits its outcome from its last
// defer, after the client already has its result frame (DATA RACE 2026-11-10,
// captureCleanupSignals vs streamOutcome.emit of the previous test).
func TestHookCaptureHelpers_WaitForRunningHandlers(t *testing.T) {
	helpers := map[string]func(t *testing.T){
		"captureCleanupSignals": func(t *testing.T) { captureCleanupSignals(t) },
		"captureStreamHooks":    func(t *testing.T) { captureStreamHooks(t) },
		"withCapturedOnError":   func(t *testing.T) { withCapturedOnError(t) },
	}
	for name, install := range helpers {
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			h := trackHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			}))
			go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			<-entered

			installed := make(chan struct{})
			go func() { install(t); close(installed) }()
			select {
			case <-installed:
				t.Errorf("%s swapped the hooks while a tracked handler was still running", name)
			case <-time.After(100 * time.Millisecond):
			}
			unblock()
			<-installed
		})
	}
}
