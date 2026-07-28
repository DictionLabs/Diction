package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	streamTimeout   = 3 * time.Hour
	wsCloseUnknown  = 4000
	wsCloseDown     = 4001
	wsCloseFailed   = 4002
	wsCloseTooLarge = 4003
	wsCloseNoAudio  = 4004
)

// errTypeSTTError mirrors ErrTypeSTTError in gateway/metrics.go. Kept as a
// local constant because the ErrType* closed vocabulary lives in the private
// main package; core/ cannot import it.
const errTypeSTTError = "stt_error"

type streamAction struct {
	Action   string `json:"action"`
	Language string `json:"language,omitempty"`
}

type streamResult struct {
	Text string `json:"text"`
	Mode string `json:"mode,omitempty"`
}

// Reason — closed vocabulary for ws_read close classification. Kept in sync
// with the `reason` tag constants in gateway/metrics.go (Reason*).
const (
	wsReasonEOF           = "eof"
	wsReasonGoingAway     = "going_away"
	wsReasonIdleTimeout   = "idle_timeout"
	wsReasonStreamTimeout = "stream_timeout"
	wsReasonProtocol      = "protocol"
	wsReasonUnknown       = "unknown"
)

// ClassifyWSError maps a conn.Read error to a closed-vocabulary reason tag.
// Idle-timeout classification is done by the caller via the external
// time.AfterFunc watchdog (see the main read loop); when this function sees
// a context error it always means the outer 90-min stream cap fired or the
// HTTP request ctx was canceled by the framework.
func ClassifyWSError(err error) string {
	if err == nil {
		return ""
	}
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case websocket.StatusGoingAway:
			return wsReasonGoingAway
		case websocket.StatusProtocolError, websocket.StatusInvalidFramePayloadData,
			websocket.StatusUnsupportedData, websocket.StatusMandatoryExtension:
			return wsReasonProtocol
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return wsReasonStreamTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return wsReasonEOF
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "EOF"):
		return wsReasonEOF
	case strings.Contains(s, "StatusGoingAway"), strings.Contains(s, "going away"):
		return wsReasonGoingAway
	case strings.Contains(s, "protocol"):
		return wsReasonProtocol
	}
	return wsReasonUnknown
}

// CloseWSWithTimeout calls conn.Close with a bounded write budget so a
// NAT-orphaned half-open socket cannot re-introduce the multi-minute hang
// we are trying to end. defer conn.CloseNow() remains as a final safety net.
func CloseWSWithTimeout(conn *websocket.Conn, code websocket.StatusCode, reason string, budget time.Duration) {
	done := make(chan struct{})
	go func() {
		_ = conn.Close(code, reason)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
	}
}

// StreamingHandler returns the handler for WS /v1/audio/stream.
//
// Protocol:
//
//	Client connects: ws(s)://host/v1/audio/stream?language=en
//	Client → Server: binary frames of PCM audio (16-bit LE, mono, 16kHz)
//	Client → Server: text frame {"action":"done"}
//	Server → Client: text frame {"text":"transcribed text"}
//	Server closes connection.
func (g *Gateway) StreamingHandler() http.HandlerFunc {
	return g.StreamingHandlerWithPostProcess(nil)
}

// StreamingHandlerWithPostProcess is like StreamingHandler but calls postProcess
// on the transcript when ?enhance=true is requested. Pass nil for no post-processing.
// postProcess receives (ctx, transcript, contextJSON, intent) and returns (resultText, mode, error).
func (g *Gateway) StreamingHandlerWithPostProcess(postProcess func(context.Context, string, string, string) (string, string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Resolve backend before upgrade — route to best model for the language.
		// Auto-detect: when ?language=auto is present, route to a detect-capable
		// model and instruct proxyToBackend to omit the language field upstream
		// so native LID can run. Old clients send a concrete code and fall
		// through to existing routing.
		language := r.URL.Query().Get("language")
		var (
			model                 string
			stripUpstreamLanguage bool
			detectActive          = IsAutoDetect(language)
			adResult              AutoDetectResult
		)
		if detectActive {
			var adCtx AutoDetectContext
			if g.DeviceHashFromContext != nil {
				adCtx.DeviceHash = g.DeviceHashFromContext(r.Context())
			}
			if adCtx.DeviceHash != "" && g.profileStore != nil {
				adCtx.Profile = g.profileStore.GetProfile(r.Context(), adCtx.DeviceHash)
			}
			adResult = g.ModelForAutoDetect(adCtx)
			if adResult.Model != "" {
				model = adResult.Model
				stripUpstreamLanguage = adResult.UpstreamLanguage == ""
			}
		}
		if model == "" {
			model = g.ModelForLanguage(language)
		}
		target, backend := g.resolveBackend(model)
		if target == nil || (!backend.SkipHealthCheck && !g.health.get(model)) {
			http.Error(w, `{"error":"backend unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		if g.fallbackModel != "" {
			log.Printf("Route: language=%s detect=%v → model=%s",
				language, detectActive, model)
		}
		w.Header().Set("X-Diction-Route-Lang", language)
		w.Header().Set("X-Diction-Route-Model", model)
		if detectActive {
			w.Header().Set("X-Diction-Route-Detect", "true")
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // allow any origin for now
		})
		if err != nil {
			log.Printf("ws accept: %v", err)
			if OnError != nil {
				OnError(r.Context(), ErrorEvent{
					Source:   "streaming",
					Kind:     "ws_accept",
					Endpoint: "/v1/audio/stream",
					Hint:     "websocket accept failed",
				})
			}
			if OnRequestFailed != nil {
				OnRequestFailed(r.Context(), errTypeSTTError)
			}
			return
		}
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), streamTimeout)
		defer cancel()

		// Collect PCM chunks; first text frame (if not a done/action) is context JSON
		var pcmBuf bytes.Buffer
		var contextJSON string
		maxPCM := g.maxBodySize
		contextRead := false
		var audioBytesReceived int64

		codec := r.URL.Query().Get("codec")
		isOpus := codec == "opus"
		// If the backend doesn't strictly need WAV, we can safely passthrough the Ogg stream directly
		usePassthrough := isOpus && !backend.NeedsWAV

		var ffmpegStdin io.WriteCloser
		ffmpegDone := make(chan struct{})
		var opusBuf bytes.Buffer

		if isOpus && !usePassthrough {
			cmd := exec.CommandContext(ctx, "ffmpeg", "-i", "pipe:0", "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
			var err error
			ffmpegStdin, err = cmd.StdinPipe()
			if err == nil {
				var stdout io.ReadCloser
				stdout, err = cmd.StdoutPipe()
				if err == nil {
					err = cmd.Start()
					if err == nil {
						go func() {
							io.Copy(&pcmBuf, stdout)
							cmd.Wait()
							close(ffmpegDone)
						}()
					}
				}
			}
			if err != nil {
				log.Printf("ws ffmpeg start: %v", err)
				CloseWSWithTimeout(conn, websocket.StatusInternalError, "ffmpeg error", 2*time.Second)
				return
			}
		} else {
			close(ffmpegDone)
		}

		idleTimeout := g.streamIdleTimeout
		if idleTimeout <= 0 {
			idleTimeout = defaultStreamIdleTimeout
		}

		for {
			// Idle watchdog: if no frame arrives within idleTimeout, call
			// conn.Close from a goroutine so a close frame is written before
			// the underlying connection is torn down. A per-frame
			// context.WithTimeout is NOT usable here: coder/websocket's
			// context.AfterFunc hook calls c.close() on ctx expiry, which
			// kills TCP before we can send StatusPolicyViolation.
			idleFired := make(chan struct{})
			idleTimer := time.AfterFunc(idleTimeout, func() {
				CloseWSWithTimeout(conn, websocket.StatusPolicyViolation, "idle_timeout", 2*time.Second)
				close(idleFired)
			})
			msgType, data, err := conn.Read(ctx)
			stopped := idleTimer.Stop()
			// Stop returns false if the timer already fired — wait for the
			// callback to finish so we know the close frame has been written
			// (or its 2s budget is exhausted).
			if !stopped {
				<-idleFired
			}
			if err != nil {
				log.Printf("ws read: %v", err)
				var reason string
				if !stopped {
					reason = wsReasonIdleTimeout
				} else {
					reason = ClassifyWSError(err)
				}
				if OnError != nil {
					OnError(ctx, ErrorEvent{
						Source:   "streaming",
						Kind:     "ws_read",
						Reason:   reason,
						Endpoint: "/v1/audio/stream",
						Hint:     "websocket read failed: " + reason,
					})
				}
				if OnRequestFailed != nil {
					OnRequestFailed(ctx, errTypeSTTError)
				}
				if stopped {
					// Non-idle error: issue our own bounded close.
					CloseWSWithTimeout(conn, websocket.StatusInternalError, reason, 2*time.Second)
				}
				return
			}
			if !stopped {
				// Race: a valid frame arrived as the idle timer fired. From
				// the user's perspective the stream terminated due to idle
				// timeout — emit the matching ws_read/idle_timeout error so
				// dashboards don't see an orphan success=false request.
				if OnError != nil {
					OnError(ctx, ErrorEvent{
						Source:   "streaming",
						Kind:     "ws_read",
						Reason:   wsReasonIdleTimeout,
						Endpoint: "/v1/audio/stream",
						Hint:     "websocket read failed: " + wsReasonIdleTimeout,
					})
				}
				if OnRequestFailed != nil {
					OnRequestFailed(ctx, errTypeSTTError)
				}
				return
			}

			if msgType == websocket.MessageBinary {
				audioBytesReceived += int64(len(data))
				if audioBytesReceived > maxPCM {
					if OnRequestFailed != nil {
						OnRequestFailed(ctx, errTypeSTTError)
					}
					CloseWSWithTimeout(conn, wsCloseTooLarge, "audio too large", 2*time.Second)
					return
				}
				if usePassthrough {
					opusBuf.Write(data)
				} else if isOpus {
					ffmpegStdin.Write(data)
				} else {
					pcmBuf.Write(data)
				}
				contextRead = true // context frame must come before any audio
				continue
			}

			// Text message - check for done action or context
			if msgType == websocket.MessageText {
				var action streamAction
				if err := json.Unmarshal(data, &action); err != nil {
					// Not valid JSON action - treat as context if first text frame
					if !contextRead {
						contextJSON = string(data)
						contextRead = true
					}
					continue
				}
				if action.Action == "done" {
					// Mid-stream language override is a re-routing hint for single-language
					// clients. When auto-detect is active on this connection we've already
					// committed to a detect-capable model, so honouring the hint would either
					// re-introduce a wrong code or re-route to a non-detect model. Ignore it.
					if action.Language != "" && !detectActive {
						language = action.Language
					}
					break
				}
				// First text frame without action field is context JSON
				if !contextRead && action.Action == "" {
					contextJSON = string(data)
					contextRead = true
					continue
				}
			}
		}

		if ffmpegStdin != nil {
			ffmpegStdin.Close()
			<-ffmpegDone
		}

		if pcmBuf.Len() == 0 && opusBuf.Len() == 0 {
			if OnRequestFailed != nil {
				OnRequestFailed(ctx, errTypeSTTError)
			}
			CloseWSWithTimeout(conn, wsCloseNoAudio, "no audio received", 2*time.Second)
			return
		}

		// Wrap PCM in WAV header and POST to backend.
		// canary_confident: inject known language code (e.g. "cs") for best accuracy.
		// Other detect tiers: strip language so the model runs native auto-LID.
		// Non-detect: pass through the client's language unchanged.
		upstreamLanguage := language
		if adResult.UpstreamLanguage != "" {
			upstreamLanguage = adResult.UpstreamLanguage
		} else if stripUpstreamLanguage {
			upstreamLanguage = ""
		}
		var audioData []byte
		if usePassthrough {
			audioData = opusBuf.Bytes()
		} else {
			audioData = pcmBuf.Bytes()
		}

		sttStart := time.Now()
		text, err := g.proxyToBackend(ctx, target, audioData, backend, upstreamLanguage)
		sttMs := time.Since(sttStart).Milliseconds()
		if err == nil && hasDegenerateRepetition(text) {
			err = errSTTHallucination
		}
		if err != nil {
			log.Printf("ws proxy: %v", err)
			kind := "stt_backend_error"
			hint := "backend transcription failed"
			if errors.Is(err, errSTTHallucination) {
				kind = "stt_hallucination"
				hint = "backend returned repeated-token hallucination"
			}
			if OnError != nil {
				OnError(ctx, ErrorEvent{
					Source:   "stt",
					Kind:     kind,
					Endpoint: "/v1/audio/stream",
					Provider: backend.Name,
					Hint:     hint,
				})
			}
			if OnRequestFailed != nil {
				OnRequestFailed(ctx, errTypeSTTError)
			}
			CloseWSWithTimeout(conn, wsCloseFailed, "transcription failed", 2*time.Second)
			return
		}

		// Report the successful transcription on the same hook the HTTP path uses,
		// so this path gets stt_ms. Side effect: audio_duration_ms was 0 on every
		// stream row until now, and output_chars was 0 on the non-enhance ones
		// (the enhance closure below already set it on the rest).
		enhanceEnabled := r.URL.Query().Get("enhance") == "true"
		if g.OnTranscription != nil {
			audioDurationMs := int64(pcmBuf.Len()) * 1000 / pcmBytesPerSecond
			g.OnTranscription(ctx, backend.Name, sttMs, len(text), audioDurationMs, enhanceEnabled, false)
		}

		// Apply post-processing if provided (e.g. ?enhance=true)
		var mode string
		if postProcess != nil && enhanceEnabled && text != "" {
			intent := r.URL.Query().Get("intent")
			if resultText, resultMode, err := postProcess(ctx, text, contextJSON, intent); err == nil {
				text = resultText
				mode = resultMode
			} else {
				log.Printf("ws post-process: %v", err)
				if OnError != nil {
					OnError(ctx, ErrorEvent{
						Source:     "stt",
						Kind:       "stt_post_process",
						Endpoint:   "/v1/audio/stream",
						InputChars: len(text),
						Hint:       "streaming post-process failed; returning raw",
					})
				}
				// Do not call OnRequestFailed — raw transcript is still returned below.
			}
		}

		result, _ := json.Marshal(streamResult{Text: text, Mode: mode})
		if err := conn.Write(ctx, websocket.MessageText, result); err != nil {
			log.Printf("ws write result: %v", err)
			if OnRequestFailed != nil {
				OnRequestFailed(ctx, errTypeSTTError)
			}
			return
		}

		CloseWSWithTimeout(conn, websocket.StatusNormalClosure, "", 2*time.Second)
	}
}

// proxyToBackend wraps raw PCM in a WAV (or passes through Ogg) and POSTs multipart to the backend.
func (g *Gateway) proxyToBackend(ctx context.Context, target *url.URL, audioData []byte, backend *Backend, language string) (string, error) {
	var payload io.Reader
	filename := "audio.wav"

	if bytes.HasPrefix(audioData, []byte("OggS")) {
		filename = "audio.ogg"
		payload = bytes.NewReader(audioData)
	} else if bytes.HasPrefix(audioData, []byte("\x1a\x45\xdf\xa3")) { // EBML magic bytes (WebM)
		filename = "audio.webm"
		payload = bytes.NewReader(audioData)
	} else {
		var wav bytes.Buffer
		if err := WriteWAVHeader(&wav, len(audioData)); err != nil {
			return "", fmt.Errorf("write wav header: %w", err)
		}
		wav.Write(audioData)
		payload = &wav
	}

	// Build multipart body
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(filePart, payload); err != nil {
		return "", fmt.Errorf("copy audio: %w", err)
	}

	if backend.ForwardModel != "" {
		writer.WriteField("model", backend.ForwardModel)
	}
	if language != "" {
		writer.WriteField("language", language)
	}
	writer.Close()

	// POST to backend
	transcriptionPath := "/v1/audio/transcriptions"
	if backend.TargetPath != "" {
		transcriptionPath = backend.TargetPath
	}
	backendURL := target.String() + transcriptionPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if backend.AuthHeader != "" {
		req.Header.Set("Authorization", backend.AuthHeader)
	}

	client := &http.Client{Timeout: 90 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("backend request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("backend returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return result.Text, nil
}
