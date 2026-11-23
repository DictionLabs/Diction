package core

import (
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

func (c *transcriptionCall) giveUp(rec *httptest.ResponseRecorder) {
	reportRequestFailed(c.r.Context())
	flushRecorder(rec, c.w)
}

func (c *transcriptionCall) rewriteForRetry(retryBackend *Backend) bool {
	converted, newCT, _, err := rewriteMultipart(c.body, c.boundary, c.rewriteOpts(retryBackend, false))
	if errors.Is(err, errDecodeBusy) {
		c.replyDecodeBusy(fmt.Sprintf("Audio decode saturated for retry backend %s; rejecting request", retryBackend.Name),
			retryBackend.Name, "audio decode semaphore saturated (retry)")
		return false
	}
	if err != nil {
		log.Printf("Multipart rewrite for retry backend %s failed: %v — using original body", retryBackend.Name, err)
		return true
	}
	c.proxyBody = converted
	c.proxyContentType = newCT
	return true
}

func (c *transcriptionCall) runRetry(retryModel string, retryTarget *url.URL, retryBackend *Backend) {
	if c.boundary != "" && !c.rewriteForRetry(retryBackend) {
		return
	}

	c.whisperStart = time.Now()
	retryRec := httptest.NewRecorder()
	c.buildProxy(retryTarget, retryBackend).ServeHTTP(retryRec, c.r)

	retryRec.Header().Set("X-Diction-Route-Retry", valueTrue)
	retryRec.Header().Set("X-Diction-Route-Model", retryModel)

	if retryRec.Code >= 500 {
		log.Printf("Retry backend %s also returned %d", retryBackend.Name, retryRec.Code)
		reportRequestFailed(c.r.Context())
	}

	c.w.Header().Del("X-Diction-Route-Model")
	flushRecorder(retryRec, c.w)
}

func (c *transcriptionCall) retryAfterHallucination(rec *httptest.ResponseRecorder) {
	retryModel := c.g.hallucinationRetryModel(c.model, c.backend, c.adResult.UpstreamLanguage, c.language, c.detectActive)
	var retryTarget *url.URL
	var retryBackend *Backend
	if retryModel != "" {
		retryTarget, retryBackend = c.g.resolveBackend(retryModel)
	}
	if retryTarget == nil {
		log.Printf("STT backend %s returned a degenerate transcript — no alternative, failing request without demotion", c.backend.Name)
		c.giveUp(rec)
		return
	}
	log.Printf("STT backend %s returned a degenerate transcript — retrying on %s without demotion", c.backend.Name, retryBackend.Name)
	c.runRetry(retryModel, retryTarget, retryBackend)
}

func (c *transcriptionCall) pickRetryModel() string {
	if c.detectActive && c.adResult.Model != "" {
		retryResult := c.g.ModelForAutoDetect(c.adCtx)
		if retryResult.Model != "" && retryResult.Model != c.model {
			return retryResult.Model
		}
	}
	return c.g.ModelForLanguage(c.language)
}

func (c *transcriptionCall) retryAfter5xx(rec *httptest.ResponseRecorder) {
	log.Printf("STT backend %s returned %d — marking unhealthy, attempting retry", c.backend.Name, rec.Code)
	c.g.health.demote(c.backend.Name, "5xx", 0)
	reportError(c.r.Context(), ErrorEvent{
		Source:     sourceSTT,
		Kind:       "stt_backend_5xx",
		Endpoint:   routeTranscriptions,
		Provider:   c.backend.Name,
		HTTPStatus: rec.Code,
		Hint:       fmt.Sprintf("backend returned %d; demoted and retrying", rec.Code),
	})

	retryModel := c.pickRetryModel()
	if retryModel == c.model {
		log.Printf("No alternative backend for retry (still %s) — returning %d to client", c.model, rec.Code)
		c.g.demoteAfterFailure(c.backend.Name, "5xx", false)
		c.giveUp(rec)
		return
	}

	retryTarget, retryBackend := c.g.resolveBackend(retryModel)
	if retryTarget == nil {
		log.Printf("Retry backend %s resolved to nil — returning original %d to client", retryModel, rec.Code)
		c.g.demoteAfterFailure(c.backend.Name, "5xx", false)
		c.giveUp(rec)
		return
	}

	c.g.demoteAfterFailure(c.backend.Name, "5xx", true)
	log.Printf("Retrying with fallback backend %s (was %s)", retryBackend.Name, c.backend.Name)

	c.runRetry(retryModel, retryTarget, retryBackend)
}

func (g *Gateway) hallucinationRetryModel(model string, primary *Backend, upstreamLang, language string, detectActive bool) string {
	lang := strings.TrimSpace(language)
	if detectActive && upstreamLang != "" {
		lang = upstreamLang
	}
	for _, m := range []string{g.fallbackModel, g.defaultModel, g.englishModel} {
		if m == "" || strings.EqualFold(m, model) {
			continue
		}
		if _, b := g.resolveBackend(m); b == nil || b.Name == primary.Name {
			continue
		}
		if !g.healthy(m) {
			continue
		}
		if lang != "" && g.langCapabilityRouting && !g.backendServes(m, lang) {
			continue
		}
		return m
	}
	return ""
}
