package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

const statusClientClosed = 499

const (
	fieldModel          = "model"
	fieldResponseFormat = "response_format"
)

var sttBackendTransport = &http.Transport{
	MaxIdleConns:          20,
	MaxIdleConnsPerHost:   5,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute,
}

type postProcessFunc = func(context.Context, string, string, string) (string, string, error)

type transcriptionCall struct {
	g           *Gateway
	postProcess postProcessFunc
	w           http.ResponseWriter
	r           *http.Request

	body           []byte
	contentType    string
	boundary       string
	language       string
	responseFormat string

	model         string
	target        *url.URL
	backend       *Backend
	detectActive  bool
	adCtx         AutoDetectContext
	adResult      AutoDetectResult
	isWhisperTier bool

	contextJSON     string
	whisperPrompt   string
	customWordsJSON string

	proxyBody        []byte
	proxyContentType string
	audioDurationMs  int64
	e2eClientKey     string
	enhanceEnabled   bool
	whisperStart     time.Time
	hallucinated     bool
}

func (g *Gateway) TranscriptionHandler() http.HandlerFunc {
	return g.TranscriptionHandlerWithPostProcess(nil)
}

func (g *Gateway) TranscriptionHandlerWithPostProcess(postProcess postProcessFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &transcriptionCall{g: g, postProcess: postProcess, w: w, r: r}
		if !c.readRequest() || !c.route() || !c.prepareBody() {
			return
		}
		c.serve()
	}
}

func reportError(ctx context.Context, e ErrorEvent) {
	if OnError != nil {
		OnError(ctx, e)
	}
}

func reportRequestFailed(ctx context.Context) {
	if OnRequestFailed != nil {
		OnRequestFailed(ctx, errTypeSTTError)
	}
}

func (c *transcriptionCall) readRequest() bool {
	if c.r.Method != http.MethodPost {
		http.Error(c.w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return false
	}

	body, err := io.ReadAll(io.LimitReader(c.r.Body, c.g.maxBodySize+1))
	if err != nil {
		http.Error(c.w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return false
	}
	if int64(len(body)) > c.g.maxBodySize {
		http.Error(c.w, fmt.Sprintf(`{"error":"request body exceeds %d bytes"}`, c.g.maxBodySize), http.StatusRequestEntityTooLarge)
		return false
	}
	c.body = body

	c.contentType = c.r.Header.Get("Content-Type")
	c.boundary = parseBoundary(c.contentType)
	c.responseFormat = formatJSON
	if c.boundary != "" {
		c.language = extractFormField(body, c.boundary, "language")
		if rf := extractFormField(body, c.boundary, fieldResponseFormat); rf != "" {
			c.responseFormat = rf
		}
	}

	switch c.responseFormat {
	case formatJSON, formatText:
	default:
		http.Error(c.w, fmt.Sprintf(`{"error":"response_format '%s' not supported; gateway supports 'json' and 'text' only"}`, c.responseFormat), http.StatusBadRequest)
		return false
	}

	if c.responseFormat == formatText && c.r.Header.Get("X-Diction-E2E") != "" {
		http.Error(c.w, `{"error":"response_format=text is incompatible with X-Diction-E2E (E2E requires JSON envelope)"}`, http.StatusBadRequest)
		return false
	}
	return true
}

func (c *transcriptionCall) pickModel() {
	c.detectActive = IsAutoDetect(c.language)
	if c.detectActive {
		if c.g.DeviceHashFromContext != nil {
			c.adCtx.DeviceHash = c.g.DeviceHashFromContext(c.r.Context())
		}
		if c.adCtx.DeviceHash != "" && c.g.profileStore != nil {
			c.adCtx.Profile = c.g.profileStore.GetProfile(c.r.Context(), c.adCtx.DeviceHash)
		}
		c.adResult = c.g.ModelForAutoDetect(c.adCtx)
		if c.adResult.Model != "" {
			c.model = c.adResult.Model
		}
	}
	if c.model == "" {
		c.model = c.g.ModelForLanguage(c.language)
	}
}

func (c *transcriptionCall) route() bool {
	c.pickModel()
	c.target, c.backend = c.g.resolveBackend(c.model)
	if c.target == nil {
		c.w.Header().Set("X-Diction-Route-Model", c.model)
		reportError(c.r.Context(), ErrorEvent{
			Source:     sourceSTT,
			Kind:       "stt_route_fail",
			Endpoint:   routeTranscriptions,
			Provider:   c.model,
			HTTPStatus: http.StatusBadRequest,
			Hint:       "no healthy backend for this language",
		})
		reportRequestFailed(c.r.Context())
		http.Error(c.w, `{"error":"backend unavailable"}`, http.StatusBadRequest)
		return false
	}
	if c.g.fallbackModel != "" {
		log.Printf("Route: language=%s detect=%v tier=%s → model=%s",
			c.language, c.detectActive, c.adResult.Tier, c.model)
	}
	if c.detectActive && c.adResult.Tier != "" && c.g.OnAutoDetect != nil {
		c.g.OnAutoDetect(c.r.Context(), c.adResult.Tier, "")
	}
	c.w.Header().Set("X-Diction-Route-Lang", c.language)
	c.w.Header().Set("X-Diction-Route-Model", c.model)
	if c.detectActive {
		c.w.Header().Set("X-Diction-Route-Detect", valueTrue)
	}
	return true
}

func (c *transcriptionCall) rewriteOpts(b *Backend, injectVerbose bool) MultipartRewriteOpts {
	return MultipartRewriteOpts{
		Ctx:               c.r.Context(),
		ConvertToWAV:      b.NeedsWAV,
		ForwardModel:      b.ForwardModel,
		WhisperPrompt:     c.whisperPrompt,
		CustomWordsJSON:   customWordsFor(b, c.customWordsJSON),
		StripLanguage:     c.detectActive && c.adResult.UpstreamLanguage == "",
		LanguageOverride:  c.adResult.UpstreamLanguage,
		InjectVerboseJSON: injectVerbose,
	}
}

func (c *transcriptionCall) replyDecodeBusy(logLine, providerName, hint string) {
	log.Print(logLine)
	reportError(c.r.Context(), ErrorEvent{
		Source:     sourceSTT,
		Kind:       "stt_decode_busy",
		Endpoint:   routeTranscriptions,
		Provider:   providerName,
		HTTPStatus: http.StatusServiceUnavailable,
		Hint:       hint,
	})
	c.w.Header().Set("Retry-After", "2")
	http.Error(c.w, `{"error":"server busy; try again"}`, http.StatusServiceUnavailable)
}

func (c *transcriptionCall) prepareBody() bool {
	c.proxyBody = c.body
	c.proxyContentType = c.contentType
	c.isWhisperTier = strings.HasPrefix(c.adResult.Tier, nameWhisper)
	if c.boundary != "" {
		c.contextJSON = extractFormField(c.body, c.boundary, "context")
		c.whisperPrompt = buildWhisperPrompt(c.contextJSON)
		c.customWordsJSON = buildCustomWordsField(c.contextJSON)

		converted, newCT, durationMs, err := rewriteMultipart(c.body, c.boundary,
			c.rewriteOpts(c.backend, c.detectActive && c.isWhisperTier))
		if errors.Is(err, errDecodeBusy) {
			c.replyDecodeBusy(fmt.Sprintf("Audio decode saturated for %s; rejecting request", c.backend.Name),
				c.backend.Name, "audio decode semaphore saturated")
			return false
		}
		if err != nil {
			log.Printf("Multipart rewrite failed for %s: %v", c.backend.Name, err)
			reportError(c.r.Context(), ErrorEvent{
				Source:     sourceSTT,
				Kind:       "stt_multipart",
				Endpoint:   routeTranscriptions,
				Provider:   c.backend.Name,
				HTTPStatus: http.StatusInternalServerError,
				Hint:       "multipart rewrite failed",
			})
			http.Error(c.w, `{"error":"request processing failed"}`, http.StatusInternalServerError)
			return false
		}
		c.proxyBody = converted
		c.proxyContentType = newCT
		c.audioDurationMs = durationMs
	}

	if c.g.OnAudioReceived != nil {
		c.g.OnAudioReceived(c.r.Context(), c.audioDurationMs)
	}
	c.e2eClientKey = c.r.Header.Get("X-Diction-E2E")
	c.enhanceEnabled = c.r.URL.Query().Get("enhance") == valueTrue
	return true
}

func (c *transcriptionCall) serve() {
	c.whisperStart = time.Now()
	rec := httptest.NewRecorder()
	c.buildProxy(c.target, c.backend).ServeHTTP(rec, c.r)

	if rec.Code < 500 {
		flushRecorder(rec, c.w)
		return
	}
	if c.hallucinated {
		c.retryAfterHallucination(rec)
		return
	}
	c.retryAfter5xx(rec)
}
