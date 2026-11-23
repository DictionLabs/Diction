package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	transcriptrules "github.com/DictionLabs/Diction/gateway/core/transcript"
)

type backendTranscription struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

func (c *transcriptionCall) buildProxy(proxyTarget *url.URL, proxyBackend *Backend) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := pr.Out
			req.URL.Scheme = proxyTarget.Scheme
			req.URL.Host = proxyTarget.Host
			path := routeTranscriptions
			if proxyBackend.TargetPath != "" {
				path = proxyBackend.TargetPath
			}
			req.URL.Path = path
			req.Host = proxyTarget.Host
			req.Body = io.NopCloser(bytes.NewReader(c.proxyBody))
			req.ContentLength = int64(len(c.proxyBody))
			c.keepDirectorParity(pr, proxyBackend.AuthHeader)
		},
		ModifyResponse: func(resp *http.Response) error {
			return c.modifyResponse(resp, proxyBackend)
		},
		Transport: sttBackendTransport,
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			c.handleProxyError(rw, req, err, proxyBackend)
		},
	}
}

// keepDirectorParity makes Rewrite send what Director did: hop-by-hop removal ran after Director.
func (c *transcriptionCall) keepDirectorParity(pr *httputil.ProxyRequest, auth string) {
	named := connectionNamedHeaders(pr.In.Header)
	if pr.In.Form == nil {
		pr.Out.URL.RawQuery = pr.In.URL.RawQuery
	}
	if !named["Content-Type"] {
		pr.Out.Header.Set("Content-Type", c.proxyContentType)
	}
	if auth != "" && !named["Authorization"] {
		pr.Out.Header.Set("Authorization", auth)
	}
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if vals, ok := pr.In.Header[h]; ok && !named[h] {
			pr.Out.Header[h] = slices.Clone(vals)
		}
	}
	ip, _, err := net.SplitHostPort(pr.In.RemoteAddr)
	prior, hasPrior := pr.Out.Header["X-Forwarded-For"]
	if err != nil || (hasPrior && prior == nil) {
		return
	}
	if len(prior) > 0 {
		ip = strings.Join(prior, ", ") + ", " + ip
	}
	pr.Out.Header.Set("X-Forwarded-For", ip)
}

func connectionNamedHeaders(h http.Header) map[string]bool {
	named := map[string]bool{}
	for _, line := range h["Connection"] {
		for _, f := range strings.Split(line, ",") {
			if f = strings.Trim(f, " \t\r\n"); f != "" {
				named[http.CanonicalHeaderKey(f)] = true
			}
		}
	}
	return named
}

func (c *transcriptionCall) handleProxyError(rw http.ResponseWriter, req *http.Request, err error, proxyBackend *Backend) {
	kind := "stt_backend_error"
	hint := "backend transport failed"
	status := http.StatusBadGateway
	if errors.Is(err, context.Canceled) {
		kind = "stt_upstream_canceled"
		hint = "upstream canceled (client disconnect or new request)"
		status = statusClientClosed
	} else if errors.Is(err, transcriptrules.ErrSTTHallucination) {
		c.hallucinated = true
		kind = "stt_hallucination"
		hint = "backend returned repeated-token hallucination"
	}
	log.Printf("http: proxy error: %v", err)
	reportError(req.Context(), ErrorEvent{
		Source:     sourceSTT,
		Kind:       kind,
		Endpoint:   routeTranscriptions,
		Provider:   proxyBackend.Name,
		HTTPStatus: status,
		Hint:       hint,
	})
	rw.WriteHeader(status)
}

func readBackendTranscription(resp *http.Response) ([]byte, backendTranscription, bool) {
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, backendTranscription{}, false
	}
	var transcription backendTranscription
	if err := json.Unmarshal(respBody, &transcription); err != nil || transcription.Text == "" {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, backendTranscription{}, false
	}
	return respBody, transcription, true
}

func (c *transcriptionCall) recordDetectedLanguage(resp *http.Response, tr backendTranscription, respBody []byte) []byte {
	if !c.detectActive || !c.isWhisperTier || tr.Language == "" {
		return respBody
	}
	if wrapped, marshalErr := json.Marshal(map[string]string{"text": tr.Text}); marshalErr == nil {
		respBody = wrapped
		setResponseBody(resp, respBody)
	}
	if c.adCtx.DeviceHash != "" && c.g.profileStore != nil {
		go c.g.profileStore.RecordLanguage(c.adCtx.DeviceHash, tr.Language)
	}
	if c.g.OnAutoDetect != nil {
		c.g.OnAutoDetect(resp.Request.Context(), "", tr.Language)
	}
	return respBody
}

func (c *transcriptionCall) stripPromptEcho(text string) (string, bool) {
	if c.whisperPrompt == "" {
		return text, false
	}
	stripped := stripWhisperPromptEcho(text)
	return stripped, stripped != text
}

func (c *transcriptionCall) needsRewrite(proxyBackend *Backend, fillerChanged, promptEchoStripped bool) bool {
	return (c.postProcess != nil && c.enhanceEnabled) || c.e2eClientKey != "" || proxyBackend.TargetPath != "" ||
		c.responseFormat != formatJSON || fillerChanged || promptEchoStripped
}

func (c *transcriptionCall) modifyResponse(resp *http.Response, proxyBackend *Backend) error {
	whisperMs := time.Since(c.whisperStart).Milliseconds()
	resp.Header.Set("X-Diction-Whisper-Ms", fmt.Sprintf("%d", whisperMs))

	if resp.StatusCode != http.StatusOK {
		return nil
	}
	respBody, tr, ok := readBackendTranscription(resp)
	if !ok {
		return nil
	}
	respBody = c.recordDetectedLanguage(resp, tr, respBody)

	transcript, promptEchoStripped := c.stripPromptEcho(tr.Text)
	if promptEchoStripped && transcript == "" {
		writeTranscriptionResponse(resp, "", "", "", c.responseFormat, c.e2eClientKey, false)
		return nil
	}
	if transcriptrules.HasDegenerateRepetition(transcript) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return transcriptrules.ErrSTTHallucination
	}
	if c.g.OnTranscription != nil {
		c.g.OnTranscription(
			resp.Request.Context(), proxyBackend.Name, whisperMs, len(transcript),
			c.audioDurationMs, c.enhanceEnabled, c.e2eClientKey != "",
		)
	}

	effectiveLang := c.language
	if c.detectActive && tr.Language != "" {
		effectiveLang = tr.Language
	}
	transcript, fillerChanged := transcriptrules.CleanTranscript(transcript, effectiveLang, proxyBackend.Provider)

	if !c.needsRewrite(proxyBackend, fillerChanged, promptEchoStripped) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil
	}

	var mode, status string
	autoEdit := false
	if c.postProcess != nil && c.enhanceEnabled {
		res := c.enhance(resp, proxyBackend, transcript, effectiveLang)
		transcript, mode, status, autoEdit = res.transcript, res.mode, res.status, res.autoEdit
	}

	writeTranscriptionResponse(resp, transcript, mode, status, c.responseFormat, c.e2eClientKey, autoEdit && mode == intentEdit)
	return nil
}

type enhanceResult struct {
	transcript string
	mode       string
	status     string
	autoEdit   bool
}

func (c *transcriptionCall) enhance(resp *http.Response, proxyBackend *Backend, transcript, effectiveLang string) enhanceResult {
	res := enhanceResult{transcript: transcript}
	enhanceContextJSON := WithContextLanguage(c.contextJSON, effectiveLang)
	intent, auto := resolveIntent(resp.Request.Context(), c.r, transcript, enhanceContextJSON, c.r.URL.Query().Get("intent"))
	res.autoEdit = auto
	llmStart := time.Now()
	ppCtx := WithSTTBoosted(resp.Request.Context(), customWordsFor(proxyBackend, c.customWordsJSON) != "")
	resultText, resultMode, err := c.postProcess(ppCtx, transcript, enhanceContextJSON, intent)
	llmMs := time.Since(llmStart).Milliseconds()
	resp.Header.Set("X-Diction-LLM-Ms", fmt.Sprintf("%d", llmMs))
	if err == nil {
		res.transcript = resultText
		res.mode = resultMode
		return res
	}
	if intent == intentEdit || intent == intentEditSelected {
		hint := "edit intent post-process failed; returning status=failed"
		log.Printf("post-process error (%s): %v", hint, err)
		reportError(resp.Request.Context(), ErrorEvent{
			Source:     sourceSTT,
			Kind:       "stt_post_process",
			Endpoint:   routeTranscriptions,
			Provider:   proxyBackend.Name,
			HTTPStatus: resp.StatusCode,
			InputChars: len(transcript),
			LatencyMs:  time.Since(llmStart).Milliseconds(),
			Hint:       hint,
		})
		res.transcript = ""
		res.mode = intent
		res.status = statusFailed
		return res
	}
	log.Printf("post-process skipped; returning raw transcript: %v", err)
	ReportEnhanceSkipped(resp.Request.Context(), EnhanceSkipReason(resp.Request.Context(), err))
	return res
}
