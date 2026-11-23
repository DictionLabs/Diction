package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The community build's enhance budgets, pinned at the wiring level.

// budgetProbe returns a closure that reports the deadline it was handed.
func budgetProbe(seen *time.Duration, hadDeadline *bool) func(context.Context, string, string, string) (string, string, error) {
	return func(ctx context.Context, _, _, _ string) (string, string, error) {
		dl, ok := ctx.Deadline()
		*hadDeadline = ok
		if ok {
			*seen = time.Until(dl)
		}
		return "enhanced", "", nil
	}
}

func TestEnhanceBudget_AppliesTimeout(t *testing.T) {
	var seen time.Duration
	var hadDeadline bool
	wrapped := enhanceBudget(budgetProbe(&seen, &hadDeadline), 20000, "inline")

	if _, _, err := wrapped(context.Background(), "text", "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hadDeadline {
		t.Fatal("expected a deadline on the context, got none")
	}
	// Allow slack for scheduling; the point is that it is ~20s, not ~8s or ~2.5s.
	if seen < 19*time.Second || seen > 20*time.Second {
		t.Errorf("expected ~20s budget, got %v", seen)
	}
}

// A non-positive budget must mean "no limit", not an already-expired context.
func TestEnhanceBudget_NonPositiveMeansUnbounded(t *testing.T) {
	for _, ms := range []int{0, -1} {
		var seen time.Duration
		var hadDeadline bool
		wrapped := enhanceBudget(budgetProbe(&seen, &hadDeadline), ms, "inline")

		out, _, err := wrapped(context.Background(), "text", "", "")
		if err != nil {
			t.Fatalf("ms=%d: unexpected error: %v", ms, err)
		}
		if out != "enhanced" {
			t.Fatalf("ms=%d: closure did not run, got %q", ms, out)
		}
		if hadDeadline {
			t.Errorf("ms=%d: expected an unbounded context, got a deadline of %v", ms, seen)
		}
	}
}

// An expired budget must surface as an error the caller can fall back on.
func TestEnhanceBudget_ExpiredBudgetErrors(t *testing.T) {
	slow := func(ctx context.Context, _, _, _ string) (string, string, error) {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(2 * time.Second):
			return "too late", "", nil
		}
	}
	wrapped := enhanceBudget(slow, 20, "inline")
	if _, _, err := wrapped(context.Background(), "text", "", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// The two budgets must not be swapped: inline 20s, post-delivery 8s.
func TestEnhanceBudget_InlineIsLongerThanPostDelivery(t *testing.T) {
	if defaultEnhanceTimeoutMs <= defaultLiveEnhanceTimeoutMs {
		t.Fatalf("inline budget (%d) must exceed post-delivery (%d): the inline path serves"+
			" self-hosted local LLMs that cloud's short budget would starve",
			defaultEnhanceTimeoutMs, defaultLiveEnhanceTimeoutMs)
	}
	// Post-delivery above ~9s is unreachable: the app's enhanced-frame budget is a hardcoded 9s.
	if defaultLiveEnhanceTimeoutMs > 9000 {
		t.Errorf("post-delivery budget %dms exceeds the client's 9s frame budget — unreachable",
			defaultLiveEnhanceTimeoutMs)
	}
}

// Not tested here: nil closures without an LLM; core's split-enhance tests pin that layer.
