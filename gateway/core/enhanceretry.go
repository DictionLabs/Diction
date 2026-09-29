package core

import (
	"context"
	"errors"
	"log"
	"time"
)

// Retry for the Writing Tools pass on the audio routes, inside the pass's own budget.
//
// The owner's rule (2026-09-27): if Clean up fails, try again while there is still time;
// if it is not on time, the user silently gets the raw transcript. The budget itself
// (inline or post-delivery, ARCHITECTURE.md invariant 3) never grows — every attempt
// shares one deadline. Plan: .claude/plans/silent-cleanup-retry-plan.md.

// EnhanceFunc is the shape of the Writing Tools pass: text in, (text, mode) out.
type EnhanceFunc = func(ctx context.Context, text, contextJSON, intent string) (string, string, error)

// MaxEnhanceAttempts is the first attempt plus two retries.
const MaxEnhanceAttempts = 3

// minEnhanceRetryBudget is the least budget that must remain for a retry to start.
// Successful cleanups take a median of 537 ms (14 d to 2026-09-27, n=4061), so with
// less than this left fewer than half of them could finish — the call would more
// likely be billed and cut off than land.
const minEnhanceRetryBudget = 500 * time.Millisecond

// WithEnhanceBudget bounds fn with one deadline for the whole pass and retries an
// attempt that failed fast, up to MaxEnhanceAttempts, while the parent context is
// alive and at least minEnhanceRetryBudget remains.
//
// There is deliberately no per-attempt cap: the attempts that run into the deadline
// are long dictations whose cleanup needs longer than the budget (median 98 s of
// audio), and a second copy would be no faster. Capping attempt one would only turn
// today's slow successes into failures.
//
// onDone (may be nil) is called once, with the attempts made and the pass wall-clock.
func WithEnhanceBudget(
	fn EnhanceFunc,
	budget time.Duration,
	onDone func(ctx context.Context, attempts int, elapsed time.Duration),
) EnhanceFunc {
	return func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		start := time.Now()
		passCtx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()

		var (
			out, mode string
			err       error
			attempts  int
		)
		for attempts < MaxEnhanceAttempts {
			attempts++
			out, mode, err = fn(passCtx, text, contextJSON, intent)
			if err == nil || !retryAllowed(ctx, passCtx) {
				break
			}
			if attempts < MaxEnhanceAttempts {
				log.Printf("enhance retry %d/%d after %v: %v",
					attempts+1, MaxEnhanceAttempts, time.Since(start).Round(time.Millisecond), err)
			}
		}
		if onDone != nil {
			onDone(ctx, attempts, time.Since(start))
		}
		return out, mode, err
	}
}

// retryAllowed reports whether another attempt may start: the client is still there,
// the pass deadline has not fired, and enough of it is left to plausibly finish.
func retryAllowed(parent, pass context.Context) bool {
	if parent.Err() != nil || pass.Err() != nil {
		return false
	}
	deadline, ok := pass.Deadline()
	return !ok || time.Until(deadline) >= minEnhanceRetryBudget
}

// Enhance skip reasons — the closed vocabulary of the `enhance_skipped` field on the
// request's own `requests` row. A skip is not an error: the user got the raw transcript,
// exactly as if Clean up were off for that one dictation.
const (
	EnhanceSkipTimeout       = "timeout"          // the pass budget ran out
	EnhanceSkipError         = "error"            // every attempt that fit failed fast
	EnhanceSkipCanceled      = "canceled"         // the client went away
	EnhanceSkipRejected      = "rejected"         // deletion guard (split socket, Live)
	EnhanceSkipNoTranscript  = "no_transcript"    // Live: nothing to clean up
	EnhanceSkipNoReadyToStop = "no_ready_to_stop" // Live: upstream never finalized
	EnhanceSkipNoLLM         = "no_llm"           // Live: no LLM configured
)

// EnhanceSkipReason classifies a failed pass for the `enhance_skipped` field.
func EnhanceSkipReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return EnhanceSkipCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return EnhanceSkipTimeout
	default:
		return EnhanceSkipError
	}
}
