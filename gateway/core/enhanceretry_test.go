package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// scriptedEnhance returns an EnhanceFunc that plays one step per call. A step either
// fails after `delay`, succeeds after `delay`, or (block) waits for its context to end.
type enhanceStep struct {
	delay time.Duration
	err   error
	block bool
}

type scriptedEnhance struct {
	mu        sync.Mutex
	steps     []enhanceStep
	calls     int
	deadlines []time.Time
}

func (s *scriptedEnhance) fn(ctx context.Context, text, _, _ string) (string, string, error) {
	s.mu.Lock()
	i := s.calls
	s.calls++
	if d, ok := ctx.Deadline(); ok {
		s.deadlines = append(s.deadlines, d)
	}
	s.mu.Unlock()
	if i >= len(s.steps) {
		return "", "", fmt.Errorf("unscripted call %d", i+1)
	}
	st := s.steps[i]
	if st.block {
		<-ctx.Done()
		return "", "", fmt.Errorf("llm request: %w", ctx.Err())
	}
	select {
	case <-time.After(st.delay):
	case <-ctx.Done():
		return "", "", fmt.Errorf("llm request: %w", ctx.Err())
	}
	if st.err != nil {
		return text, "transcribe", st.err
	}
	return "clean: " + text, "transcribe", nil
}

func (s *scriptedEnhance) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type passRecord struct {
	attempts int
	elapsed  time.Duration
	calls    int
}

func runPass(t *testing.T, ctx context.Context, s *scriptedEnhance, budget time.Duration) (string, error, passRecord) {
	t.Helper()
	var rec passRecord
	wrapped := WithEnhanceBudget(s.fn, budget, func(_ context.Context, attempts int, elapsed time.Duration) {
		rec.attempts = attempts
		rec.elapsed = elapsed
		rec.calls++
	})
	text, _, err := wrapped(ctx, "hello", "", "")
	return text, err, rec
}

var errFast = errors.New("llm api error 503: busy")

func TestWithEnhanceBudget_SuccessFirstTry(t *testing.T) {
	s := &scriptedEnhance{steps: []enhanceStep{{}}}
	text, err, rec := runPass(t, context.Background(), s, time.Second)
	if err != nil || text != "clean: hello" {
		t.Fatalf("got %q, %v", text, err)
	}
	if rec.attempts != 1 || rec.calls != 1 {
		t.Errorf("attempts=%d onDone calls=%d, want 1/1", rec.attempts, rec.calls)
	}
}

func TestWithEnhanceBudget_FastFailureThenSuccess(t *testing.T) {
	s := &scriptedEnhance{steps: []enhanceStep{{err: errFast}, {}}}
	text, err, rec := runPass(t, context.Background(), s, 2*time.Second)
	if err != nil || text != "clean: hello" {
		t.Fatalf("want the retry's success, got %q, %v", text, err)
	}
	if rec.attempts != 2 {
		t.Errorf("attempts = %d, want 2", rec.attempts)
	}
}

func TestWithEnhanceBudget_NeverMoreThanThreeAttempts(t *testing.T) {
	s := &scriptedEnhance{steps: []enhanceStep{{err: errFast}, {err: errFast}, {err: errFast}, {}}}
	_, err, rec := runPass(t, context.Background(), s, 2*time.Second)
	if !errors.Is(err, errFast) {
		t.Fatalf("want the last attempt's error, got %v", err)
	}
	if rec.attempts != MaxEnhanceAttempts || s.callCount() != MaxEnhanceAttempts {
		t.Errorf("attempts=%d calls=%d, want %d", rec.attempts, s.callCount(), MaxEnhanceAttempts)
	}
}

// A slow attempt that runs into the deadline is not retried, and the pass never
// outlives its budget.
func TestWithEnhanceBudget_DeadlineIsNotRetriedOrExtended(t *testing.T) {
	s := &scriptedEnhance{steps: []enhanceStep{{block: true}, {}}}
	start := time.Now()
	_, err, rec := runPass(t, context.Background(), s, 200*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if rec.attempts != 1 {
		t.Errorf("attempts = %d, want 1", rec.attempts)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("pass took %v, want it bounded by the 200ms budget", elapsed)
	}
}

// Retries share the one pass deadline: a retry after a fast failure does not get a
// fresh budget of its own.
func TestWithEnhanceBudget_RetriesShareOneDeadline(t *testing.T) {
	s := &scriptedEnhance{steps: []enhanceStep{{err: errFast, delay: 50 * time.Millisecond}, {block: true}}}
	budget := 700 * time.Millisecond
	start := time.Now()
	_, err, rec := runPass(t, context.Background(), s, budget)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if rec.attempts != 2 {
		t.Errorf("attempts = %d, want 2", rec.attempts)
	}
	if elapsed > budget+300*time.Millisecond {
		t.Errorf("pass took %v, want it to end at the %v pass deadline", elapsed, budget)
	}
	if len(s.deadlines) != 2 || !s.deadlines[0].Equal(s.deadlines[1]) {
		t.Errorf("attempt deadlines %v, want both attempts to share one", s.deadlines)
	}
}

// With less than minEnhanceRetryBudget left after a failure, no further attempt starts.
func TestWithEnhanceBudget_NoRetryBelowTheFloor(t *testing.T) {
	budget := minEnhanceRetryBudget + 200*time.Millisecond
	s := &scriptedEnhance{steps: []enhanceStep{{err: errFast, delay: 300 * time.Millisecond}, {}}}
	_, err, rec := runPass(t, context.Background(), s, budget)
	if !errors.Is(err, errFast) {
		t.Fatalf("want the first attempt's error, got %v", err)
	}
	if rec.attempts != 1 || s.callCount() != 1 {
		t.Errorf("attempts=%d calls=%d, want no retry below the floor", rec.attempts, s.callCount())
	}
}

// A client that went away gets no retries.
func TestWithEnhanceBudget_NoRetryWhenParentCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scriptedEnhance{steps: []enhanceStep{{err: errFast}, {}}}
	wrapped := WithEnhanceBudget(func(c context.Context, text, cj, in string) (string, string, error) {
		cancel()
		return s.fn(c, text, cj, in)
	}, 2*time.Second, nil)
	_, _, err := wrapped(ctx, "hello", "", "")
	if err == nil {
		t.Fatal("want an error")
	}
	if s.callCount() != 1 {
		t.Errorf("calls = %d, want 1 (no retry after the client left)", s.callCount())
	}
}

func TestEnhanceSkipReason(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"deadline", context.Background(), fmt.Errorf("llm request: %w", context.DeadlineExceeded), EnhanceSkipTimeout},
		{"client gone (ctx)", cancelled, errFast, EnhanceSkipCanceled},
		{"client gone (err)", context.Background(), fmt.Errorf("llm request: %w", context.Canceled), EnhanceSkipCanceled},
		{"llm error", context.Background(), errFast, EnhanceSkipError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EnhanceSkipReason(tt.ctx, tt.err); got != tt.want {
				t.Errorf("EnhanceSkipReason = %q, want %q", got, tt.want)
			}
		})
	}
}

// cleanupSignals captures what a handler reported about a Writing Tools pass: error
// events (kind) and enhance skips (reason). Installed per test, restored on cleanup.
type cleanupSignals struct {
	mu         sync.Mutex
	errorKinds []string
	skips      []string
}

func captureCleanupSignals(t *testing.T) *cleanupSignals {
	t.Helper()
	c := &cleanupSignals{}
	prevErr, prevSkip := OnError, OnEnhanceSkipped
	OnError = func(_ context.Context, e ErrorEvent) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.errorKinds = append(c.errorKinds, e.Kind)
	}
	OnEnhanceSkipped = func(_ context.Context, reason string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.skips = append(c.skips, reason)
	}
	t.Cleanup(func() { OnError, OnEnhanceSkipped = prevErr, prevSkip })
	return c
}

// assertSilentSkip checks a transcribe-intent pass that did not land: exactly one skip
// with the wanted reason, and no stt_post_process error event.
func (c *cleanupSignals) assertSilentSkip(t *testing.T, wantReason string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.errorKinds {
		if k == "stt_post_process" {
			t.Errorf("a transcribe-intent cleanup that did not land must not write an error event, got kinds %v", c.errorKinds)
		}
	}
	if len(c.skips) != 1 || c.skips[0] != wantReason {
		t.Errorf("enhance skips = %v, want exactly [%s]", c.skips, wantReason)
	}
}
