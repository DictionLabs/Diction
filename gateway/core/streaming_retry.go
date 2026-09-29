package core

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"
)

// Backend fallback for /v1/audio/stream.
//
// The HTTP path (proxy.go) has always demoted a failing STT backend AND retried the
// request on whatever routing picks next; the socket path only demoted, so the user's
// dictation was lost even with a healthy alternative up (2026-08-26 redeploy: 50 failed
// streams in a minute for one user). By the time the backend is called the audio is
// fully buffered, so the retry is one more proxyToBackend call.
//
// The client bounds all of this: StreamingClient waits 5 s for the result after `done`
// and then falls back to HTTP itself. A retry that lands after that is dropped in the
// existing ws_write branch, which is not counted as a failure.
// Plan: .claude/plans/stream-fallback-cleanup-signal-plan.md (D1–D5).

// streamRetryInput is what the handler already knows that a retry needs to repeat
// attempt 1's routing and upstream request.
type streamRetryInput struct {
	model            string
	language         string
	detectActive     bool
	adResult         AutoDetectResult
	adCtx            AutoDetectContext
	payload          audioPayload
	usePassthrough   bool
	upstreamLanguage string
	whisperPrompt    string
}

// isRetryableBackendFailure reports whether a proxyToBackend error means the backend
// is sick (5xx) or unreachable (transport fault), as opposed to our own request being
// wrong (4xx), the client leaving (Canceled), a stall (DeadlineExceeded), a response we
// could not decode, or a hallucination — which depends on the audio, so another backend
// is not obviously better and demoting a healthy one for 120 s would be worse.
func isRetryableBackendFailure(err error) bool {
	if err == nil || errors.Is(err, errSTTHallucination) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if code := backendStatusFromErr(err.Error()); code > 0 {
		return code >= 500
	}
	return strings.HasPrefix(err.Error(), "backend request:")
}

// handleStreamBackendFailure reports the primary failure and, when it is retryable,
// demotes the backend and retries once on the backend routing now picks. Returns the
// transcript, the backend that produced it and the retry's STT time when the dictation
// was rescued (ok=true). On ok=false every event and OnRequestFailed has already been
// emitted and the caller only closes the socket.
func (g *Gateway) handleStreamBackendFailure(
	ctx context.Context, err error, backend *Backend, in streamRetryInput,
) (text string, used *Backend, retryMs int64, ok bool) {
	kind, hint, status := classifyBackendFailure(err)
	retryable := isRetryableBackendFailure(err)
	if retryable {
		// Parity with /v1/audio/transcriptions: take a sick or unreachable backend
		// out of rotation instead of sending the next dictation into the same fault.
		// Transient — startHealthChecker re-probes every 120 s and restores it.
		// Also what lets re-selection below pick something else.
		g.health.set(backend.Name, false)
	}

	var plan streamRetryPlan
	if retryable {
		plan = g.planStreamRetry(in)
	}
	if plan.backend != nil {
		hint += "; demoted, retrying on " + plan.model
	} else if retryable && kind != kindSTTBackend5xx {
		// Transport fault with nowhere to go (typically a single-backend self-hosted
		// gateway): the demotion existed only to let re-selection pick something else,
		// so undo it rather than 503 every dictation until the next health probe. A 5xx
		// keeps its demotion — that predates the retry (f9bb47ee).
		g.health.set(backend.Name, true)
	}
	emitStreamSTTError(ctx, kind, backend.Name, status, hint)

	if plan.backend == nil {
		// A client that walked away is not a backend fault and must not inflate the
		// failure rate — same rule as the HTTP path's statusClientClosed.
		if OnRequestFailed != nil && kind != kindSTTUpstreamCanceled {
			OnRequestFailed(ctx, errTypeSTTError)
		}
		return "", backend, 0, false
	}

	log.Printf("ws retry: backend %s failed (%s) — retrying on %s", backend.Name, hint, plan.backend.Name)
	start := time.Now()
	text, rerr := g.proxyToBackend(ctx, plan.target, plan.payload, plan.backend, in.upstreamLanguage, in.whisperPrompt)
	retryMs = time.Since(start).Milliseconds()
	if rerr == nil && hasDegenerateRepetition(text) {
		rerr = errSTTHallucination
	}
	if rerr == nil {
		return text, plan.backend, retryMs, true
	}

	log.Printf("ws retry: %s also failed: %v", plan.backend.Name, rerr)
	rkind, rhint, rstatus := classifyBackendFailure(rerr)
	if rkind == kindSTTBackend5xx {
		g.health.set(plan.backend.Name, false)
	}
	emitStreamSTTError(ctx, rkind, plan.backend.Name, rstatus, rhint+"; retry backend")
	if OnRequestFailed != nil && rkind != kindSTTUpstreamCanceled {
		OnRequestFailed(ctx, errTypeSTTError)
	}
	return "", plan.backend, retryMs, false
}

// streamRetryPlan is a resolved retry target; backend == nil means "no retry".
type streamRetryPlan struct {
	model   string
	target  *url.URL
	backend *Backend
	payload audioPayload
}

// planStreamRetry mirrors proxy.go's retry selection: re-run auto-detect routing when
// it chose attempt 1, else language routing; give up when routing lands on the same
// model or on nothing. The payload is reused as-is unless the failed backend was fed
// raw Ogg/WebM (passthrough) and the retry backend needs WAV.
func (g *Gateway) planStreamRetry(in streamRetryInput) streamRetryPlan {
	retryModel := ""
	if in.detectActive && in.adResult.Model != "" {
		if r := g.ModelForAutoDetect(in.adCtx); r.Model != "" && r.Model != in.model {
			retryModel = r.Model
		}
	}
	if retryModel == "" {
		retryModel = g.ModelForLanguage(in.language)
	}
	if retryModel == in.model {
		log.Printf("ws retry: no alternative backend (still %s)", in.model)
		return streamRetryPlan{}
	}
	target, backend := g.resolveBackend(retryModel)
	if target == nil || backend == nil {
		log.Printf("ws retry: backend %s resolved to nil", retryModel)
		return streamRetryPlan{}
	}
	payload := in.payload
	if in.usePassthrough && backend.NeedsWAV {
		wav, err := convertToWAVBytes(payload.data, payload.filename)
		if err != nil {
			log.Printf("ws retry: cannot convert %s for %s: %v", payload.filename, backend.Name, err)
			return streamRetryPlan{}
		}
		payload = audioPayload{data: wav, filename: "audio.wav"}
	}
	return streamRetryPlan{model: retryModel, target: target, backend: backend, payload: payload}
}

func emitStreamSTTError(ctx context.Context, kind, provider string, status int, hint string) {
	if OnError == nil {
		return
	}
	OnError(ctx, ErrorEvent{
		Source:     "stt",
		Kind:       kind,
		Endpoint:   "/v1/audio/stream",
		Provider:   provider,
		HTTPStatus: status,
		Hint:       hint,
	})
}
