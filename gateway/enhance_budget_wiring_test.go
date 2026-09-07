package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The community build's enhance budgets, pinned at the wiring level.
//
// core/streaming_split_enhance_test.go already covers the split answer's *behaviour*,
// including the nil-post-delivery shape this build used to rely on. What it cannot see is
// whether main.go hands the handler the right two closures — and swapping them, or letting
// either reach context.WithTimeout(ctx, 0), fails in a way no user-visible error names:
// Writing Tools just quietly returns raw text forever.

// budgetProbe returns a postProcess-shaped closure that reports the deadline it was
// handed, so a wrapper's budget can be measured without waiting for it to elapse.
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

// A non-positive budget must mean "no limit", not "no time".
//
// core.EnvIntOrDefault does no range validation, so DICTION_ENHANCE_TIMEOUT_MS=0 would
// reach context.WithTimeout(ctx, 0) — an already-expired context — and fail every pass
// instantly into the raw fallback. 0 is the near-universal spelling of "unlimited", so
// the self-hoster most likely to set it would get the exact opposite, silently.
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

// An expired budget must surface as an error the caller can fall back on, never as a
// silently truncated result.
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

// The two budgets must not be swapped. Inline (20s) bounds the user's wait; post-delivery
// (8s) runs after the raw text is already on screen. Reversing them would make the user
// wait 20s for text they could have had in 8, while capping the free wait at 2.5s-style
// tightness — and nothing in the wire contract would complain.
func TestEnhanceBudget_InlineIsLongerThanPostDelivery(t *testing.T) {
	if defaultEnhanceTimeoutMs <= defaultLiveEnhanceTimeoutMs {
		t.Fatalf("inline budget (%d) must exceed post-delivery (%d): the inline path serves"+
			" self-hosted local LLMs that cloud's short budget would starve",
			defaultEnhanceTimeoutMs, defaultLiveEnhanceTimeoutMs)
	}
	// Post-delivery above ~9s is unreachable: the iOS client's enhanced-frame budget is a
	// hardcoded 9s and is not backend-aware, so it has already stopped listening.
	if defaultLiveEnhanceTimeoutMs > 9000 {
		t.Errorf("post-delivery budget %dms exceeds the client's 9s frame budget — unreachable",
			defaultLiveEnhanceTimeoutMs)
	}
}

// NOT TESTED HERE, deliberately: that both closures stay nil when no LLM is configured.
//
// core/streaming.go gates the split answer on `postProcess != nil`, so a wrapped nil would
// turn "no LLM configured" into "an LLM that always fails" — a real hazard, and the reason
// both assignments live inside a single `if llm.Enabled` block in buildMux rather than
// being wrapped afterwards. But `postProcess` is a local inside buildMux and is not
// observable from a test without standing up a fake STT backend and driving a real
// WebSocket through the mux.
//
// A test that declared its own variables and asserted on those would pass forever while
// proving nothing, so it is left out rather than written badly. The behaviour it would
// cover is already pinned one layer down by core's TestSplitEnhance_AbsentParamKeepsSingleFrame
// and TestSplitEnhance_NilPostDeliveryFallsBackToInline.
