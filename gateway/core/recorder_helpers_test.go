package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var activeHandlers sync.WaitGroup

func trackHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeHandlers.Add(1)
		defer activeHandlers.Done()
		h.ServeHTTP(w, r)
	})
}

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

func (r *hookRecorder[T]) All() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.items...)
}

func (r *hookRecorder[T]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

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
