package core

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/DictionLabs/Diction/gateway/core/transcript"
)

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
	customWordsJSON  string
}

func isRetryableBackendFailure(err error) bool {
	if err == nil || errors.Is(err, transcript.ErrSTTHallucination) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if code := backendStatusFromErr(err.Error()); code > 0 {
		return code >= 500
	}
	return strings.HasPrefix(err.Error(), "backend request:")
}

func (g *Gateway) handleStreamBackendFailure(
	ctx context.Context, err error, backend *Backend, in streamRetryInput,
) (text string, used *Backend, retryMs int64, ok bool) {
	kind, hint, status := classifyBackendFailure(err)
	retryable := isRetryableBackendFailure(err)
	var demotion demotionToken
	if retryable {
		demotion = g.health.demote(backend.Name, demotionReason(kind), 0)
	}

	var plan streamRetryPlan
	if retryable {
		plan = g.planStreamRetry(in)
	}
	switch {
	case plan.backend != nil:
		hint += "; demoted, retrying on " + plan.model
		g.demoteAfterFailure(backend.Name, demotionReason(kind), true)
	case retryable && kind == kindSTTBackend5xx:
		g.demoteAfterFailure(backend.Name, demotionReason(kind), false)
	case retryable:
		g.health.undoDemotion(backend.Name, demotion)
	}
	emitStreamSTTError(ctx, kind, backend.Name, status, hint)

	if plan.backend == nil {
		reportStreamFailed(ctx, kind)
		return "", backend, 0, false
	}

	log.Printf("ws retry: backend %s failed (%s) — retrying on %s", backend.Name, hint, plan.backend.Name)
	return g.retryStreamOnBackend(ctx, plan, in)
}

func reportStreamFailed(ctx context.Context, kind string) {
	if OnRequestFailed != nil && kind != kindSTTUpstreamCanceled {
		OnRequestFailed(ctx, errTypeSTTError)
	}
}

func (g *Gateway) retryStreamOnBackend(
	ctx context.Context, plan streamRetryPlan, in streamRetryInput,
) (text string, used *Backend, retryMs int64, ok bool) {
	start := time.Now()
	text, rerr := g.proxyToBackend(ctx, plan.target, plan.payload, plan.backend, in.upstreamLanguage, in.whisperPrompt, in.customWordsJSON)
	retryMs = time.Since(start).Milliseconds()
	if rerr == nil && transcript.HasDegenerateRepetition(text) {
		rerr = transcript.ErrSTTHallucination
	}
	if rerr == nil {
		return text, plan.backend, retryMs, true
	}

	log.Printf("ws retry: %s also failed: %v", plan.backend.Name, rerr)
	rkind, rhint, rstatus := classifyBackendFailure(rerr)
	if rkind == kindSTTBackend5xx {
		g.demoteAfterFailure(plan.backend.Name, "5xx", false)
	}
	emitStreamSTTError(ctx, rkind, plan.backend.Name, rstatus, rhint+"; retry backend")
	reportStreamFailed(ctx, rkind)
	return "", plan.backend, retryMs, false
}

func demotionReason(kind string) string {
	if kind == kindSTTBackend5xx {
		return "5xx"
	}
	return "transport"
}

type streamRetryPlan struct {
	model   string
	target  *url.URL
	backend *Backend
	payload audioPayload
}

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
		payload = audioPayload{data: wav, filename: fileNameWav}
	}
	return streamRetryPlan{model: retryModel, target: target, backend: backend, payload: payload}
}

func emitStreamSTTError(ctx context.Context, kind, provider string, status int, hint string) {
	if OnError == nil {
		return
	}
	OnError(ctx, ErrorEvent{
		Source:     sourceSTT,
		Kind:       kind,
		Endpoint:   routeAudioStream,
		Provider:   provider,
		HTTPStatus: status,
		Hint:       hint,
	})
}
