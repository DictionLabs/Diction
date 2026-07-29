package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// statusClientClosed mirrors nginx's 499 "Client Closed Request": the client
// went away (disconnect or a fresh request superseding this one) before the
// upstream STT backend responded. It is deliberately sub-500 so the retry path
// treats it as a non-server-fault — no health demotion, no stt_backend_5xx, no
// wasteful fallback GPU inference for a client that is already gone.
const statusClientClosed = 499

// sttBackendTransport is the shared HTTP transport used to forward STT
// requests from the gateway to the backend (canary, whisper-large-turbo, …).
//
// Keeping this at package scope lets the idle connection pool actually do its
// job — when it was created per-request inside the handler closure, every STT
// call paid a fresh TCP handshake to the backend. Container-to-container RTT
// is sub-millisecond, but pooled keep-alives still save a few ms and remove
// unnecessary socket churn under load.
//
// ResponseHeaderTimeout is intentionally generous: live transcription /
// post-processing can sit on the connection for several minutes.
var sttBackendTransport = &http.Transport{
	MaxIdleConns:          20,
	MaxIdleConnsPerHost:   5,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute,
}

// parseBoundary extracts the multipart boundary from the Content-Type header.
func parseBoundary(contentType string) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return ""
	}
	return params["boundary"]
}

// MultipartRewriteOpts captures the optional flags that rewriteMultipart accepts. Used
// instead of a growing list of bool params now that we've added `stripLanguage` to the
// existing convertToWAV/forwardModel/whisperPrompt combination.
type MultipartRewriteOpts struct {
	ConvertToWAV  bool
	ForwardModel  string
	WhisperPrompt string
	StripLanguage bool // when true, drop the upstream `language` field (auto-detect / parakeet tiers)

	// LanguageOverride, when non-empty, replaces the upstream language field with this code.
	// Used for canary_confident tier: strip the client's "auto" and inject the known language code.
	// Mutually exclusive with StripLanguage — LanguageOverride takes precedence.
	LanguageOverride string

	// InjectVerboseJSON, when true, appends response_format=verbose_json to the forwarded
	// request so Whisper returns the detected language code in its response.
	// Only set for Whisper-tier auto-detect paths; Parakeet and Canary must not receive it.
	InjectVerboseJSON bool
}

// rewriteMultipart rewrites the multipart body:
// - Strips the "model", "context", and "response_format" fields (gateway-owned)
// - If opts.StripLanguage is true, also strips the "language" field (auto-detect mode)
// - If opts.ForwardModel is non-empty, injects it for the backend
// - If opts.WhisperPrompt is non-empty, injects it as a "prompt" field for vocab hinting
// - If opts.ConvertToWAV is true, converts the audio file to 16kHz mono WAV via ffmpeg
func rewriteMultipart(body []byte, boundary string, opts MultipartRewriteOpts) ([]byte, string, int64, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	var audioDurationMs int64

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", 0, fmt.Errorf("read multipart: %w", err)
		}

		// Strip gateway-owned fields. Always: model, context, response_format.
		// Conditionally: language (auto-detect strip or override — both remove the client value).
		name := part.FormName()
		if name == "model" || name == "context" || name == "response_format" {
			part.Close()
			continue
		}
		if name == "language" && (opts.StripLanguage || opts.LanguageOverride != "") {
			part.Close()
			continue
		}

		if name == "file" {
			audioData, err := io.ReadAll(part)
			filename := part.FileName()
			part.Close()
			if err != nil {
				return nil, "", 0, fmt.Errorf("read audio: %w", err)
			}

			if opts.ConvertToWAV {
				audioData, err = convertToWAVBytes(audioData, filename)
				if err != nil {
					return nil, "", 0, err
				}
				filename = "audio.wav"
			}

			audioDurationMs = ParseWAVDurationMs(audioData)

			partHeader := make(textproto.MIMEHeader)
			partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
			if opts.ConvertToWAV {
				partHeader.Set("Content-Type", "audio/wav")
			}
			dst, err := writer.CreatePart(partHeader)
			if err != nil {
				return nil, "", 0, fmt.Errorf("create file part: %w", err)
			}
			if _, err := dst.Write(audioData); err != nil {
				return nil, "", 0, fmt.Errorf("write audio: %w", err)
			}
		} else {
			// Copy non-file parts as-is
			data, err := io.ReadAll(part)
			part.Close()
			if err != nil {
				return nil, "", 0, fmt.Errorf("read part %s: %w", name, err)
			}
			if err := writer.WriteField(name, string(data)); err != nil {
				return nil, "", 0, fmt.Errorf("write field %s: %w", name, err)
			}
		}
	}

	if opts.ForwardModel != "" {
		if err := writer.WriteField("model", opts.ForwardModel); err != nil {
			return nil, "", 0, fmt.Errorf("write model field: %w", err)
		}
	}

	if opts.WhisperPrompt != "" {
		if err := writer.WriteField("prompt", opts.WhisperPrompt); err != nil {
			return nil, "", 0, fmt.Errorf("write prompt field: %w", err)
		}
	}

	if opts.LanguageOverride != "" {
		if err := writer.WriteField("language", opts.LanguageOverride); err != nil {
			return nil, "", 0, fmt.Errorf("write language override field: %w", err)
		}
	}

	if opts.InjectVerboseJSON {
		if err := writer.WriteField("response_format", "verbose_json"); err != nil {
			return nil, "", 0, fmt.Errorf("write response_format field: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, "", 0, fmt.Errorf("close multipart writer: %w", err)
	}

	return buf.Bytes(), writer.FormDataContentType(), audioDurationMs, nil
}

// convertToWAVBytes converts audio bytes to 16kHz mono WAV. Passes through data that's already WAV.
func convertToWAVBytes(audioData []byte, filename string) ([]byte, error) {
	// Skip conversion if already WAV
	if len(audioData) >= 4 && string(audioData[:4]) == "RIFF" {
		return audioData, nil
	}

	tmpDir, err := os.MkdirTemp("", "diction-convert-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	ext := ".m4a"
	if filename != "" {
		if e := filepath.Ext(filename); e != "" {
			ext = e
		}
	}

	inPath := filepath.Join(tmpDir, "input"+ext)
	outPath := filepath.Join(tmpDir, "output.wav")

	if err := os.WriteFile(inPath, audioData, 0644); err != nil {
		return nil, fmt.Errorf("write temp input: %w", err)
	}

	cmd := exec.Command("ffmpeg", "-y", "-i", inPath, "-ar", "16000", "-ac", "1", "-f", "wav", outPath)
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg convert: %w", err)
	}

	wavData, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read wav output: %w", err)
	}

	return wavData, nil
}

// buildWhisperPrompt extracts custom words from the transcription context JSON and formats
// them as a Whisper prompt field. Whisper uses this as vocabulary hint during transcription,
// improving accuracy for unusual terms, jargon, and proper nouns before LLM cleanup runs.
func buildWhisperPrompt(contextJSON string) string {
	if contextJSON == "" {
		return ""
	}
	var ctx struct {
		CustomWords []struct {
			Word string `json:"word"`
		} `json:"customWords"`
	}
	if err := json.Unmarshal([]byte(contextJSON), &ctx); err != nil || len(ctx.CustomWords) == 0 {
		return ""
	}
	words := make([]string, 0, len(ctx.CustomWords))
	for _, cw := range ctx.CustomWords {
		if cw.Word != "" {
			words = append(words, cw.Word)
		}
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, ", ")
}

// extractFormField reads a single field value from a multipart body without consuming it.
func extractFormField(body []byte, boundary, fieldName string) string {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FormName() == fieldName {
			data, _ := io.ReadAll(part)
			part.Close()
			return string(data)
		}
		part.Close()
	}
	return ""
}

// writeTranscriptionResponse rewrites the proxied response body into the shape
// requested by the client. Called only on a successful (2xx) backend response;
// backend errors pass through untouched in whatever shape the backend produced.
//
// responseFormat must be "json" (default) or "text" (OpenAI-compatible plain body).
// e2eClientKey is the raw X-Diction-E2E header — when set, JSON output is encrypted.
// "text" + e2eClientKey != "" is rejected upstream in the handler.
func writeTranscriptionResponse(resp *http.Response, transcript, mode, responseFormat, e2eClientKey string) {
	// Plain text — OpenAI response_format=text. Only reachable when e2eClientKey == "".
	if responseFormat == "text" {
		resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
		body := []byte(transcript)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		return
	}

	// E2E JSON — wrap transcript in encrypted envelope.
	if e2eClientKey != "" {
		ct, pk, err := EncryptTranscript(transcript, e2eClientKey)
		if err == nil {
			result := map[string]any{
				"e2e": map[string]string{"ct": ct, "pk": pk},
			}
			if mode != "" {
				result["mode"] = mode
			}
			newBody, _ := json.Marshal(result)
			resp.Body = io.NopCloser(bytes.NewReader(newBody))
			resp.ContentLength = int64(len(newBody))
			resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
			return
		}
		log.Printf("e2e encrypt error: %v", err)
		if OnError != nil {
			OnError(resp.Request.Context(), ErrorEvent{
				Source:     "e2e",
				Kind:       "e2e_encrypt",
				Endpoint:   "/v1/audio/transcriptions",
				HTTPStatus: resp.StatusCode,
				Hint:       "transcript e2e encrypt failed; falling back to plain",
			})
		}
		// fall through to plain JSON
	}

	// Plain JSON (default).
	result := map[string]string{"text": transcript}
	if mode != "" {
		result["mode"] = mode
	}
	newBody, _ := json.Marshal(result)
	resp.Body = io.NopCloser(bytes.NewReader(newBody))
	resp.ContentLength = int64(len(newBody))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
}

// TranscriptionHandler returns the handler for POST /v1/audio/transcriptions.
func (g *Gateway) TranscriptionHandler() http.HandlerFunc {
	return g.TranscriptionHandlerWithPostProcess(nil)
}

// TranscriptionHandlerWithPostProcess is like TranscriptionHandler but calls postProcess
// on the transcript when ?enhance=true is requested. Pass nil for no post-processing.
// postProcess receives (ctx, transcript, contextJSON, intent) and returns (resultText, mode, error).
func (g *Gateway) TranscriptionHandlerWithPostProcess(postProcess func(context.Context, string, string, string) (string, string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// Read body (bounded)
		body, err := io.ReadAll(io.LimitReader(r.Body, g.maxBodySize+1))
		if err != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}
		if int64(len(body)) > g.maxBodySize {
			http.Error(w, fmt.Sprintf(`{"error":"request body exceeds %d bytes"}`, g.maxBodySize), http.StatusRequestEntityTooLarge)
			return
		}

		// Resolve backend — route to best model for the request's language
		contentType := r.Header.Get("Content-Type")
		boundary := parseBoundary(contentType)
		language := ""
		responseFormat := "json"
		if boundary != "" {
			language = extractFormField(body, boundary, "language")
			if rf := extractFormField(body, boundary, "response_format"); rf != "" {
				responseFormat = rf
			}
		}

		// Only OpenAI formats we can actually produce. verbose_json/srt/vtt require
		// word timestamps which aren't universally available; fail loud rather than
		// silently returning a different shape than the client asked for.
		switch responseFormat {
		case "json", "text":
		default:
			http.Error(w, fmt.Sprintf(`{"error":"response_format '%s' not supported; gateway supports 'json' and 'text' only"}`, responseFormat), http.StatusBadRequest)
			return
		}

		// response_format=text returns a raw string body — no room for the E2E envelope.
		// iOS never sends response_format; Speaches clients never send X-Diction-E2E;
		// this is a configuration error, not a silent-fallback situation.
		if responseFormat == "text" && r.Header.Get("X-Diction-E2E") != "" {
			http.Error(w, `{"error":"response_format=text is incompatible with X-Diction-E2E (E2E requires JSON envelope)"}`, http.StatusBadRequest)
			return
		}
		// Auto-detect routing: when the client sent `language=auto`, route using per-device
		// language history. Cold start → whisper_safe; EU history → parakeet_history;
		// dominant EU → canary_confident. Old clients with a concrete language code fall
		// through to existing ModelForLanguage routing unchanged.
		var (
			model        string
			detectActive = IsAutoDetect(language)
			adCtx        AutoDetectContext
			adResult     AutoDetectResult
		)
		if detectActive {
			if g.DeviceHashFromContext != nil {
				adCtx.DeviceHash = g.DeviceHashFromContext(r.Context())
			}
			if adCtx.DeviceHash != "" && g.profileStore != nil {
				adCtx.Profile = g.profileStore.GetProfile(r.Context(), adCtx.DeviceHash)
			}
			adResult = g.ModelForAutoDetect(adCtx)
			if adResult.Model != "" {
				model = adResult.Model
			}
		}
		if model == "" {
			model = g.ModelForLanguage(language)
		}
		target, backend := g.resolveBackend(model)
		if target == nil {
			http.Error(w, `{"error":"backend unavailable"}`, http.StatusBadRequest)
			return
		}
		if g.fallbackModel != "" {
			log.Printf("Route: language=%s detect=%v tier=%s → model=%s",
				language, detectActive, adResult.Tier, model)
		}
		if detectActive && adResult.Tier != "" && g.OnAutoDetect != nil {
			g.OnAutoDetect(r.Context(), adResult.Tier, "")
		}
		w.Header().Set("X-Diction-Route-Lang", language)
		w.Header().Set("X-Diction-Route-Model", model)
		if detectActive {
			w.Header().Set("X-Diction-Route-Detect", "true")
		}

		// Extract context and build Whisper vocabulary prompt from custom words
		var contextJSON string
		var whisperPrompt string
		if boundary != "" {
			contextJSON = extractFormField(body, boundary, "context")
			whisperPrompt = buildWhisperPrompt(contextJSON)
		}

		// Rewrite multipart body: strip model/context fields (routing done), convert audio if needed, inject Whisper prompt
		proxyBody := body
		proxyContentType := contentType
		var audioDurationMs int64
		isWhisperTier := strings.HasPrefix(adResult.Tier, "whisper")
		if boundary != "" {
			converted, newCT, durationMs, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
				ConvertToWAV:      backend.NeedsWAV,
				ForwardModel:      backend.ForwardModel,
				WhisperPrompt:     whisperPrompt,
				StripLanguage:     detectActive && adResult.UpstreamLanguage == "",
				LanguageOverride:  adResult.UpstreamLanguage,
				InjectVerboseJSON: detectActive && isWhisperTier,
			})
			if err != nil {
				log.Printf("Multipart rewrite failed for %s: %v", backend.Name, err)
				if OnError != nil {
					OnError(r.Context(), ErrorEvent{
						Source:     "stt",
						Kind:       "stt_multipart",
						Endpoint:   "/v1/audio/transcriptions",
						Provider:   backend.Name,
						HTTPStatus: http.StatusInternalServerError,
						Hint:       "multipart rewrite failed",
					})
				}
				http.Error(w, `{"error":"request processing failed"}`, http.StatusInternalServerError)
				return
			}
			proxyBody = converted
			proxyContentType = newCT
			audioDurationMs = durationMs
		}

		// Capture E2E client key before proxy (X-Diction-E2E header carries client ephemeral X25519 pubkey)
		e2eClientKey := r.Header.Get("X-Diction-E2E")

		// buildProxy creates a ReverseProxy configured for the given backend/target.
		// whisperStart is captured once and shared across attempts so the total
		// latency header reflects the full wall-clock time including retries.
		enhanceEnabled := r.URL.Query().Get("enhance") == "true"
		whisperStart := time.Now()

		buildProxy := func(proxyTarget *url.URL, proxyBackend *Backend) *httputil.ReverseProxy {
			return &httputil.ReverseProxy{
				Director: func(req *http.Request) {
					req.URL.Scheme = proxyTarget.Scheme
					req.URL.Host = proxyTarget.Host
					path := "/v1/audio/transcriptions"
					if proxyBackend.TargetPath != "" {
						path = proxyBackend.TargetPath
					}
					req.URL.Path = path
					req.Host = proxyTarget.Host
					req.Header.Set("Content-Type", proxyContentType)
					req.Body = io.NopCloser(bytes.NewReader(proxyBody))
					req.ContentLength = int64(len(proxyBody))
					if proxyBackend.AuthHeader != "" {
						req.Header.Set("Authorization", proxyBackend.AuthHeader)
					}
				},
				ModifyResponse: func(resp *http.Response) error {
					whisperMs := time.Since(whisperStart).Milliseconds()
					resp.Header.Set("X-Diction-Whisper-Ms", fmt.Sprintf("%d", whisperMs))

					if resp.StatusCode != http.StatusOK {
						return nil
					}

					// Always read the response body to extract the transcript text for metrics,
					// even when no rewrite is needed. The body is restored below.
					respBody, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						resp.Body = io.NopCloser(bytes.NewReader(respBody))
						return nil
					}

					// Parse {"text": "..."} — permissive: also succeeds on verbose_json
					// because Go's json.Unmarshal ignores extra fields.
					var transcription struct {
						Text     string `json:"text"`
						Language string `json:"language"`
					}
					if err := json.Unmarshal(respBody, &transcription); err != nil || transcription.Text == "" {
						resp.Body = io.NopCloser(bytes.NewReader(respBody))
						return nil
					}

					// Whisper verbose_json path: re-wrap as {"text":"..."} before any
					// further processing so the client never sees verbose_json. Also
					// record the detected language into the device profile.
					if detectActive && isWhisperTier && transcription.Language != "" {
						if wrapped, marshalErr := json.Marshal(map[string]string{"text": transcription.Text}); marshalErr == nil {
							respBody = wrapped
						}
						if adCtx.DeviceHash != "" && g.profileStore != nil {
							go g.profileStore.RecordLanguage(adCtx.DeviceHash, transcription.Language)
						}
						if g.OnAutoDetect != nil {
							g.OnAutoDetect(resp.Request.Context(), "", transcription.Language)
						}
					}

					transcript := transcription.Text
					if hasDegenerateRepetition(transcript) {
						resp.Body = io.NopCloser(bytes.NewReader(respBody))
						return errSTTHallucination
					}
					if g.OnTranscription != nil {
						g.OnTranscription(resp.Request.Context(), proxyBackend.Name, whisperMs, len(transcript), audioDurationMs, enhanceEnabled, e2eClientKey != "")
					}

					// Deterministic filler + repetition removal — the always-on "verbatim
					// baseline" every tier gets (cloud, cloud-trial, self-hosted). Prefer the
					// STT backend's actually-detected language over the nominal `language`
					// field, which stays the literal "auto" forever under auto-detect and
					// would otherwise silently disable filler removal for those requests.
					effectiveLang := language
					if detectActive && transcription.Language != "" {
						effectiveLang = transcription.Language
					}
					cleaned, fillerChanged := CleanTranscript(transcript, effectiveLang, proxyBackend.Provider)
					transcript = cleaned

					// Backends with a custom TargetPath (e.g. Canary) may return extra fields
					// (e.g. "timestamps":null) that aren't part of the gateway contract — always
					// normalize their response body to {"text":"..."}.
					needsNormalize := proxyBackend.TargetPath != ""
					needsRewrite := (postProcess != nil && enhanceEnabled) || e2eClientKey != "" || needsNormalize || responseFormat != "json" || fillerChanged
					if !needsRewrite {
						resp.Body = io.NopCloser(bytes.NewReader(respBody))
						return nil
					}

					// LLM post-processing (if requested)
					var mode string
					if postProcess != nil && enhanceEnabled {
						intent := r.URL.Query().Get("intent")
						llmStart := time.Now()
						resultText, resultMode, err := postProcess(resp.Request.Context(), transcript, contextJSON, intent)
						llmMs := time.Since(llmStart).Milliseconds()
						resp.Header.Set("X-Diction-LLM-Ms", fmt.Sprintf("%d", llmMs))
						if err != nil {
							log.Printf("post-process error (returning raw): %v", err)
							if OnError != nil {
								OnError(resp.Request.Context(), ErrorEvent{
									Source:     "stt",
									Kind:       "stt_post_process",
									Endpoint:   "/v1/audio/transcriptions",
									Provider:   proxyBackend.Name,
									HTTPStatus: resp.StatusCode,
									InputChars: len(transcript),
									LatencyMs:  time.Since(llmStart).Milliseconds(),
									Hint:       "post-process failed; returning raw transcript",
								})
							}
						} else {
							transcript = resultText
							mode = resultMode
						}
					}

					writeTranscriptionResponse(resp, transcript, mode, responseFormat, e2eClientKey)
					return nil
				},
				Transport: sttBackendTransport,
				ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
					kind := "stt_backend_error"
					hint := "backend transport failed"
					// Default to 502 so genuine backend faults enter the
					// caller's demote-and-retry path. A pure client cancel is
					// NOT a backend fault, so it writes a sub-500 status
					// (see statusClientClosed) and short-circuits that path.
					status := http.StatusBadGateway
					if errors.Is(err, context.Canceled) {
						kind = "stt_upstream_canceled"
						hint = "upstream canceled (client disconnect or new request)"
						status = statusClientClosed
					} else if errors.Is(err, errSTTHallucination) {
						kind = "stt_hallucination"
						hint = "backend returned repeated-token hallucination"
					}
					log.Printf("http: proxy error: %v", err)
					if OnError != nil {
						OnError(req.Context(), ErrorEvent{
							Source:     "stt",
							Kind:       kind,
							Endpoint:   "/v1/audio/transcriptions",
							Provider:   proxyBackend.Name,
							HTTPStatus: status,
							Hint:       hint,
						})
					}
					// Note: OnRequestFailed is NOT called here — the caller
					// (ResponseRecorder retry path) now owns that decision so
					// a successful retry doesn't inflate the failure count. A
					// client cancel writes statusClientClosed (sub-500), so the
					// caller flushes and returns without demoting the backend,
					// emitting stt_backend_5xx, calling OnRequestFailed, or
					// firing a fallback retry.
					rw.WriteHeader(status)
				},
			}
		}

		// ── Attempt 1: proxy to the primary backend via ResponseRecorder ──
		rec := httptest.NewRecorder()
		buildProxy(target, backend).ServeHTTP(rec, r)

		if rec.Code < 500 {
			// Success (or 4xx client error) — flush to real writer.
			flushRecorder(rec, w)
			return
		}

		// ── 5xx from primary — mark unhealthy and attempt retry ──
		log.Printf("STT backend %s returned %d — marking unhealthy, attempting retry", backend.Name, rec.Code)
		g.health.set(backend.Name, false)
		if OnError != nil {
			OnError(r.Context(), ErrorEvent{
				Source:     "stt",
				Kind:       "stt_backend_5xx",
				Endpoint:   "/v1/audio/transcriptions",
				Provider:   backend.Name,
				HTTPStatus: rec.Code,
				Hint:       fmt.Sprintf("backend returned %d; demoted and retrying", rec.Code),
			})
		}

		// Re-run model selection — the demoted backend will be skipped by health checks.
		retryModel := ""
		if detectActive && adResult.Model != "" {
			// Re-run auto-detect routing (will skip the now-unhealthy model)
			retryResult := g.ModelForAutoDetect(adCtx)
			if retryResult.Model != "" && retryResult.Model != model {
				retryModel = retryResult.Model
			}
		}
		if retryModel == "" {
			retryModel = g.ModelForLanguage(language)
		}
		if retryModel == model {
			// No alternative backend available — return the original error.
			log.Printf("No alternative backend for retry (still %s) — returning %d to client", model, rec.Code)
			if OnRequestFailed != nil {
				OnRequestFailed(r.Context(), errTypeSTTError)
			}
			flushRecorder(rec, w)
			return
		}

		retryTarget, retryBackend := g.resolveBackend(retryModel)
		if retryTarget == nil {
			log.Printf("Retry backend %s resolved to nil — returning original %d to client", retryModel, rec.Code)
			if OnRequestFailed != nil {
				OnRequestFailed(r.Context(), errTypeSTTError)
			}
			flushRecorder(rec, w)
			return
		}

		log.Printf("Retrying with fallback backend %s (was %s)", retryBackend.Name, backend.Name)

		// Re-rewrite the multipart body for the retry backend. The original
		// rewrite used the primary backend's NeedsWAV/ForwardModel; the retry
		// backend may have different settings. We use the original `body` so
		// the rewrite is clean. Auto-detect injection (verbose_json, language
		// override) is intentionally omitted — we're in degraded fallback mode,
		// not optimizing auto-detect tiers.
		if boundary != "" {
			converted, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
				ConvertToWAV:  retryBackend.NeedsWAV,
				ForwardModel:  retryBackend.ForwardModel,
				WhisperPrompt: whisperPrompt,
			})
			if err != nil {
				log.Printf("Multipart rewrite for retry backend %s failed: %v — using original body", retryBackend.Name, err)
			} else {
				proxyBody = converted
				proxyContentType = newCT
			}
		}

		// ── Attempt 2: proxy to the fallback backend ──
		// Reset whisperStart so the latency header reflects the retry attempt.
		whisperStart = time.Now()
		retryRec := httptest.NewRecorder()
		buildProxy(retryTarget, retryBackend).ServeHTTP(retryRec, r)

		// Tag the response so InfluxDB can track retry frequency.
		retryRec.Header().Set("X-Diction-Route-Retry", "true")
		retryRec.Header().Set("X-Diction-Route-Model", retryModel)

		if retryRec.Code >= 500 {
			log.Printf("Retry backend %s also returned %d", retryBackend.Name, retryRec.Code)
			if OnRequestFailed != nil {
				OnRequestFailed(r.Context(), errTypeSTTError)
			}
		}

		flushRecorder(retryRec, w)
	}
}

// flushRecorder copies the captured response from an httptest.ResponseRecorder
// to a real http.ResponseWriter.
func flushRecorder(rec *httptest.ResponseRecorder, w http.ResponseWriter) {
	for k, vals := range rec.Header() {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}
