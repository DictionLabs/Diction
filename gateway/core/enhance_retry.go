package core

import (
	"context"
	"errors"
	"log"
	"time"
)

type EnhanceFunc = func(ctx context.Context, text, contextJSON, intent string) (string, string, error)

const MaxEnhanceAttempts = 3

const minEnhanceRetryBudget = 500 * time.Millisecond

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
			if err == nil || errors.Is(err, ErrEnhanceRejected) || !retryAllowed(ctx, passCtx) {
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

func retryAllowed(parent, pass context.Context) bool {
	if parent.Err() != nil || pass.Err() != nil {
		return false
	}
	deadline, ok := pass.Deadline()
	return !ok || time.Until(deadline) >= minEnhanceRetryBudget
}

const (
	EnhanceSkipTimeout       = "timeout"
	EnhanceSkipError         = "error"
	EnhanceSkipCanceled      = "canceled"
	EnhanceSkipRejected      = "rejected"
	EnhanceSkipNoTranscript  = "no_transcript"
	EnhanceSkipNoReadyToStop = "no_ready_to_stop"
	EnhanceSkipNoLLM         = "no_llm"
)

var ErrEnhanceRejected = errors.New("enhance answer rejected by a guard")

func EnhanceSkipReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, ErrEnhanceRejected):
		return EnhanceSkipRejected
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return EnhanceSkipCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return EnhanceSkipTimeout
	default:
		return EnhanceSkipError
	}
}
