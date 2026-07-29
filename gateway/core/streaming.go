package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

	// Per-message WebSocket read limit. coder/websocket defaults to 32 KiB,
	// which is far below what a batching or whole-file client sends. 8 MiB is
	// ~44 min of 24 kb/s Opus in a single frame; the session total is still
	// bounded by maxBodySize and the decoded-size cap.
	maxWSMessageBytes = 8 << 20
)

// errTypeSTTError mirrors ErrTypeSTTError in gateway/metrics.go. Kept as a
// local constant because the ErrType* closed vocabulary lives in the private
// main package; core/ cannot import it.
const errTypeSTTError = "stt_error"

// opusSubprotocol is the WebSocket subprotocol used for Ogg/Opus negotiation.
// The client offers this in the Upgrade request; the server echoes it only when
// it can actually honour Opus decoding. Self-hosters on older gateways never
// see the echo and fall back to PCM silently.
const opusSubprotocol = "diction.opus.v1"

// ffmpegBin and opusSem are initialized once per process lifetime.
var (
	ffmpegLookMu   sync.Mutex
	ffmpegLookDone bool
	ffmpegBinPath  string // empty string means "not found"

	opusSemOnce sync.Once
	opusSemCh   chan struct{}
)

// lookupFfmpeg probes PATH for ffmpeg once and caches the result.
// Tests may call testSetFfmpegPath to override the cached value.
func lookupFfmpeg() string {
	ffmpegLookMu.Lock()
	defer ffmpegLookMu.Unlock()
	if !ffmpegLookDone {
		p, err := exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegBinPath = p
		}
		ffmpegLookDone = true
	}
	return ffmpegBinPath
}

// testSetFfmpegPath overrides the cached ffmpeg path for test isolation.
func testSetFfmpegPath(path string) {
	ffmpegLookMu.Lock()
	defer ffmpegLookMu.Unlock()
	ffmpegBinPath = path
	ffmpegLookDone = true
}

// getOpusSem returns the process-wide semaphore that limits concurrent ffmpeg
// decode processes. Sized from DICTION_OPUS_CONCURRENCY or GOMAXPROCS.
func getOpusSem() chan struct{} {
	opusSemOnce.Do(func() {
		n := runtime.GOMAXPROCS(0)
		if s := os.Getenv("DICTION_OPUS_CONCURRENCY"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				n = v
			}
		}
		opusSemCh = make(chan struct{}, n)
	})
	return opusSemCh
}

// audioPayload carries the audio bytes and the filename that proxyToBackend
// will use when building the multipart body. The caller always assembles the
// payload (including any WAV header) before calling proxyToBackend, so the
// function never needs to guess the format from magic bytes.
type audioPayload struct {
	data     []byte
	filename string
}

// limitedStderrWriter is a Write-only sink that accepts at most cap bytes and
// then silently discards. Used to bound ffmpeg stderr capture.
type limitedStderrWriter struct {
	buf bytes.Buffer
	cap int
}

func (lw *limitedStderrWriter) Write(p []byte) (int, error) {
	remaining := lw.cap - lw.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		lw.buf.Write(p) //nolint:errcheck — writing to a bytes.Buffer is infallible
	}
	return len(p), nil // always report full write so exec doesn't get a short-write error
}

// readOggGranulepos returns the granule position from the last Ogg page in
// data, or -1 if no valid page is found. The search scans backwards so it
// finds the last page without walking the whole stream.
func readOggGranulepos(data []byte) int64 {
	// OggS capture pattern (4 bytes) + stream structure version (1 byte, must be 0) = 5 bytes minimum,
	// then 9 more before granulepos ends = i+14 must be within data.
	for i := len(data) - 27; i >= 0; i-- {
		if data[i] != 'O' || data[i+1] != 'g' || data[i+2] != 'g' || data[i+3] != 'S' {
			continue
		}
		if data[i+4] != 0 { // stream structure version
			continue
		}
		if i+14 > len(data) {
			continue
		}
		// Granulepos: bytes 6–13 of the page header, int64 little-endian.
		// The special value -1 (0xFFFFFFFFFFFFFFFF) means "not yet determined";
		// skip it and keep looking backwards.
		gp := int64(binary.LittleEndian.Uint64(data[i+6 : i+14]))
		if gp != -1 {
			return gp
		}
	}
	return -1
}

// readOggPreSkip reads the pre-skip value from the OpusHead packet on the
// first Ogg page. Pre-skip is in 48 kHz samples; subtract it from granulepos
// before dividing by 48 to get milliseconds. Returns 0 on any parse failure
// (slightly overstates duration by ~6.5 ms — conservative and harmless).
func readOggPreSkip(data []byte) uint16 {
	if len(data) < 28 {
		return 0
	}
	if !bytes.Equal(data[0:4], []byte("OggS")) || data[4] != 0 {
		return 0
	}
	nseg := int(data[26])
	headerLen := 27 + nseg
	if headerLen > len(data) {
		return 0
	}
	head := data[headerLen:]
	// OpusHead: 8 bytes magic + 1 version + 1 channels + 2 pre-skip (LE)
	if len(head) < 12 || !bytes.Equal(head[0:8], []byte("OpusHead")) {
		return 0
	}
	return binary.LittleEndian.Uint16(head[10:12])
}

// oggDurationMs returns the duration of an Ogg/Opus stream in milliseconds,
// derived from the last page's granule position and the pre-skip in OpusHead.
// Returns 0 for WebM or any stream that can't be parsed (safe fallback).
func oggDurationMs(data []byte) int64 {
	gp := readOggGranulepos(data)
	if gp < 0 {
		return 0
	}
	preSkip := int64(readOggPreSkip(data))
	adjusted := gp - preSkip
	if adjusted <= 0 {
		return 0
	}
	return adjusted / 48 // granulepos is in 48 kHz ticks; /48 gives ms
}

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
//	Client connects: ws(s)://host/v1/audio/stream?language=en[&codec=opus]
//	Client → Server: binary frames of PCM audio (16-bit LE, mono, 16kHz)
//	           — or — Ogg/Opus frames when codec=opus was negotiated
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
		// --- Codec validation (before WS upgrade) ---
		// Unknown codec values get a 400 *before* the upgrade so the error is a
		// plain HTTP response rather than a close frame, making client-side
		// debugging far easier.
		codec := r.URL.Query().Get("codec")
		switch codec {
		case "", "pcm", "opus":
			// valid
		default:
			http.Error(w, `{"error":"unsupported codec; accepted values: pcm, opus"}`, http.StatusBadRequest)
			return
		}

		// --- Backend resolution (must run before websocket.Accept) ---
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

		// --- Subprotocol negotiation ---
		// NB: resolveBackend (above) MUST precede websocket.Accept so that
		// backend.NeedsWAV is known when we decide whether to offer the Opus
		// subprotocol. Reordering those two calls would silently break
		// capability negotiation.
		ffmpegAvail := lookupFfmpeg() != ""
		canOfferOpus := ffmpegAvail || !backend.NeedsWAV

		// Check whether the client offered the subprotocol in the Upgrade request
		// BEFORE Accept erases the header.
		clientOfferedOpus := strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), opusSubprotocol)

		acceptOpts := &websocket.AcceptOptions{
			InsecureSkipVerify: true, // allow any origin for now
		}
		if canOfferOpus {
			acceptOpts.Subprotocols = []string{opusSubprotocol}
		}

		conn, err := websocket.Accept(w, r, acceptOpts)
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

		// coder/websocket defaults Read to 32 KiB per message. That default was
		// never hit by our own client (PCM chunks are ~682 B every 20-30 ms), but
		// it silently kills any client that batches — which is exactly what a
		// compressed codec encourages on a bad network, and what a browser
		// MediaRecorder or a whole-file uploader does by default. The total
		// payload stays bounded by maxBodySize / the decoded-size cap below;
		// this only bounds a single message's allocation.
		conn.SetReadLimit(maxWSMessageBytes)

		ctx, cancel := context.WithTimeout(r.Context(), streamTimeout)
		defer cancel()

		// Determine effective codec for this connection.
		//
		// Three cases:
		//   1. negotiatedOpus — client offered diction.opus.v1 AND server accepted it.
		//      The connection carries an Ogg/Opus stream.
		//   2. clientOfferedOpus && !negotiatedOpus — client tried but server declined
		//      (no ffmpeg + NeedsWAV). Per the spec the client MUST fall back to PCM.
		//      We also fall back on our side so a non-conforming client gets PCM transcription.
		//   3. codec==opus, no subprotocol offered — non-negotiating client (curl, etc.)
		//      Old path: serve if we can, fail explicitly if we can't.
		negotiatedOpus := conn.Subprotocol() == opusSubprotocol
		isOpus := false
		negotiationOutcome := "" // "accepted" | "offered" | "unavailable" | ""
		if codec == "opus" {
			switch {
			case negotiatedOpus:
				isOpus = true
				negotiationOutcome = "accepted"
			case clientOfferedOpus:
				// Declined — safe PCM fallback. isOpus stays false.
				log.Printf("ws: client offered %s but server declined; falling back to PCM", opusSubprotocol)
				negotiationOutcome = "unavailable"
			default:
				// Non-negotiating client.
				if !ffmpegAvail && backend.NeedsWAV {
					log.Printf("ws: codec=opus requested but ffmpeg not in PATH")
					if OnError != nil {
						OnError(ctx, ErrorEvent{
							Source:   "streaming",
							Kind:     "opus_decode",
							Endpoint: "/v1/audio/stream",
							Hint:     "ffmpeg not found; cannot decode Opus for this backend",
						})
					}
					if OnRequestFailed != nil {
						OnRequestFailed(ctx, errTypeSTTError)
					}
					CloseWSWithTimeout(conn, wsCloseFailed, "opus unavailable: ffmpeg not in PATH", 2*time.Second)
					return
				}
				isOpus = true
			}
		} else if canOfferOpus && clientOfferedOpus && !negotiatedOpus {
			// Client offered but codec!=opus → "offered, not used" signal.
			negotiationOutcome = "offered"
		}

		// Report codec + negotiation outcome before the read loop so it lands in
		// the log entry even when the connection is aborted early.
		codecTag := "pcm"
		if isOpus {
			codecTag = "opus"
		}
		if g.OnStreamingCodec != nil {
			g.OnStreamingCodec(ctx, codecTag, negotiationOutcome)
		}

		// Passthrough: when the backend can accept Ogg/WebM directly, skip the
		// ffmpeg decode and forward the accumulated Opus bytes as-is.
		usePassthrough := isOpus && !backend.NeedsWAV

		// --- Semaphore (NeedsWAV decode path only) ---
		// Every cloud backend is NeedsWAV, so cloud traffic spawns one ffmpeg
		// process per concurrent dictation. The semaphore bounds that.
		if isOpus && backend.NeedsWAV {
			sem := getOpusSem()
			waitTimer := time.NewTimer(5 * time.Second)
			defer waitTimer.Stop()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-waitTimer.C:
				log.Printf("ws: Opus semaphore saturated; rejecting connection")
				if OnRequestFailed != nil {
					OnRequestFailed(ctx, errTypeSTTError)
				}
				CloseWSWithTimeout(conn, wsCloseFailed, "server busy; too many concurrent Opus decodes", 2*time.Second)
				return
			case <-ctx.Done():
				return
			}
		}

		// --- State for the read loop ---
		var (
			pcmBuf          bytes.Buffer
			opusInputBuf    bytes.Buffer // accumulates Ogg/WebM bytes for passthrough
			contextJSON     string
			maxPCM          = g.maxBodySize
			contextRead     bool
			audioBytesRx    int64
			containerFile   string // "audio.ogg" or "audio.webm" — set on first sniff
			containerSniffed bool

			// ffmpeg decode state — populated on first Opus binary frame
			ffmpegCmd      *exec.Cmd
			ffmpegStdin    io.WriteCloser
			ffmpegStderr   = limitedStderrWriter{cap: 4096}
			stdoutDone     chan struct{}
			ffmpegWriteErr error
		)

		// ffmpegCleanup closes stdin and waits for the process to exit + stdout to drain.
		// It is idempotent — safe to call explicitly after the loop AND again from the
		// defer so that early returns in the post-loop section also reap the process.
		var ffmpegCleaned bool
		ffmpegCleanup := func() {
			if ffmpegCleaned || ffmpegStdin == nil {
				return
			}
			ffmpegCleaned = true
			ffmpegStdin.Close()
			if stdoutDone != nil {
				<-stdoutDone
			}
			ffmpegCmd.Wait() //nolint:errcheck — we capture stderr separately
		}
		defer ffmpegCleanup()

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
				audioBytesRx += int64(len(data))
				if audioBytesRx > maxPCM {
					if OnRequestFailed != nil {
						OnRequestFailed(ctx, errTypeSTTError)
					}
					CloseWSWithTimeout(conn, wsCloseTooLarge, "audio too large", 2*time.Second)
					return
				}

				if isOpus && !containerSniffed {
					// First binary Opus frame: sniff the container to pick the right
					// ffmpeg input format and a meaningful filename.
					containerSniffed = true
					switch {
					case bytes.HasPrefix(data, []byte("OggS")):
						containerFile = "audio.ogg"
					case bytes.HasPrefix(data, []byte("\x1a\x45\xdf\xa3")):
						containerFile = "audio.webm"
					default:
						log.Printf("ws: codec=opus but first frame is neither Ogg nor WebM")
						if OnError != nil {
							OnError(ctx, ErrorEvent{
								Source:   "streaming",
								Kind:     "opus_decode",
								Endpoint: "/v1/audio/stream",
								Hint:     "expected Ogg or WebM container; got unknown magic bytes",
							})
						}
						if OnRequestFailed != nil {
							OnRequestFailed(ctx, errTypeSTTError)
						}
						CloseWSWithTimeout(conn, wsCloseUnknown, "expected Ogg or WebM container", 2*time.Second)
						return
					}

					// Start ffmpeg now that we know the container format. This deferred
					// start means `-f ogg` or `-f webm` is always explicit, preventing
					// ffmpeg from probe-blocking on a live pipe.
					if !usePassthrough {
						inputFmt := strings.TrimPrefix(containerFile, "audio.")
						cmd := exec.CommandContext(ctx, lookupFfmpeg(),
							"-loglevel", "error",
							"-f", inputFmt, "-i", "pipe:0",
							"-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
						cmd.Stderr = &ffmpegStderr

						stdin, serr := cmd.StdinPipe()
						if serr != nil {
							log.Printf("ws ffmpeg stdin pipe: %v", serr)
							CloseWSWithTimeout(conn, websocket.StatusInternalError, "ffmpeg error", 2*time.Second)
							if OnRequestFailed != nil {
								OnRequestFailed(ctx, errTypeSTTError)
							}
							return
						}
						stdout, serr := cmd.StdoutPipe()
						if serr != nil {
							log.Printf("ws ffmpeg stdout pipe: %v", serr)
							CloseWSWithTimeout(conn, websocket.StatusInternalError, "ffmpeg error", 2*time.Second)
							if OnRequestFailed != nil {
								OnRequestFailed(ctx, errTypeSTTError)
							}
							return
						}
						if serr = cmd.Start(); serr != nil {
							log.Printf("ws ffmpeg start: %v", serr)
							CloseWSWithTimeout(conn, websocket.StatusInternalError, "ffmpeg error", 2*time.Second)
							if OnRequestFailed != nil {
								OnRequestFailed(ctx, errTypeSTTError)
							}
							return
						}
						ffmpegCmd = cmd
						ffmpegStdin = stdin
						done := make(chan struct{})
						stdoutDone = done
						go func() {
							// Bound the decoded output to maxPCM+1 so an amplified
							// decode cannot exhaust memory. The +1 lets us distinguish
							// "exactly maxPCM" from "exceeded" after the drain.
							io.Copy(&pcmBuf, io.LimitReader(stdout, maxPCM+1)) //nolint:errcheck
							close(done)
						}()
					}
				}

				if usePassthrough {
					opusInputBuf.Write(data)
				} else if isOpus {
					if _, werr := ffmpegStdin.Write(data); werr != nil {
						// ffmpeg died mid-stream. Break the read loop and handle below.
						ffmpegWriteErr = werr
						break
					}
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

		// ffmpegCleanup is deferred; it runs here on normal exit and on every
		// early return below, so no explicit call is needed. The check below is
		// after cleanup has drained stdout and reaped the process.
		ffmpegCleanup()

		// After draining, handle any write error that broke the read loop.
		if ffmpegWriteErr != nil {
			hint := fmt.Sprintf("ffmpeg stdin write error: %v", ffmpegWriteErr)
			if s := strings.TrimSpace(ffmpegStderr.buf.String()); s != "" {
				hint += "; ffmpeg: " + s
			}
			log.Printf("ws opus decode: %s", hint)
			if OnError != nil {
				OnError(ctx, ErrorEvent{
					Source:   "stt",
					Kind:     "opus_decode",
					Endpoint: "/v1/audio/stream",
					Hint:     hint,
				})
			}
			if OnRequestFailed != nil {
				OnRequestFailed(ctx, errTypeSTTError)
			}
			CloseWSWithTimeout(conn, wsCloseFailed, "opus decode error", 2*time.Second)
			return
		}

		// Check the decoded-side size cap. audioBytesRx guards the compressed
		// input; this guards the amplified output. At 200 MB compressed and
		// ~16 kb/s that would be ~28 h of audio → ~3 GB decoded, so this is
		// not merely theoretical.
		if isOpus && !usePassthrough && int64(pcmBuf.Len()) > maxPCM {
			if OnRequestFailed != nil {
				OnRequestFailed(ctx, errTypeSTTError)
			}
			CloseWSWithTimeout(conn, wsCloseTooLarge, "decoded audio too large", 2*time.Second)
			return
		}

		if pcmBuf.Len() == 0 && opusInputBuf.Len() == 0 {
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

		// Build the audio payload for proxyToBackend. The caller (us) is
		// responsible for WAV wrapping — proxyToBackend just forwards whatever
		// bytes we hand it, using the filename to set Content-Disposition.
		var payload audioPayload
		var audioDurationMs int64
		if usePassthrough {
			payload = audioPayload{data: opusInputBuf.Bytes(), filename: containerFile}
			if containerFile == "audio.ogg" {
				audioDurationMs = oggDurationMs(opusInputBuf.Bytes())
			}
			// WebM: audioDurationMs stays 0 (no granulepos parser for EBML); one log line.
			if audioDurationMs == 0 && opusInputBuf.Len() > 0 {
				log.Printf("ws passthrough: could not derive duration from %s; reporting 0", containerFile)
			}
		} else {
			// PCM path (both raw PCM and decoded-from-Opus): wrap in WAV.
			var wavBuf bytes.Buffer
			if err := WriteWAVHeader(&wavBuf, pcmBuf.Len()); err != nil {
				log.Printf("ws wav header: %v", err)
				if OnRequestFailed != nil {
					OnRequestFailed(ctx, errTypeSTTError)
				}
				CloseWSWithTimeout(conn, wsCloseFailed, "internal error", 2*time.Second)
				return
			}
			wavBuf.Write(pcmBuf.Bytes())
			payload = audioPayload{data: wavBuf.Bytes(), filename: "audio.wav"}
			audioDurationMs = int64(pcmBuf.Len()) * 1000 / pcmBytesPerSecond
		}

		sttStart := time.Now()
		text, err := g.proxyToBackend(ctx, target, payload, backend, upstreamLanguage)
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

// proxyToBackend POSTs multipart audio to the whisper-compatible backend.
// The caller is responsible for preparing p.data (WAV-wrapped PCM for backends
// that need WAV, or raw Ogg/WebM for passthrough backends) so this function
// never needs to sniff magic bytes.
func (g *Gateway) proxyToBackend(ctx context.Context, target *url.URL, p audioPayload, backend *Backend, language string) (string, error) {
	// Build multipart body
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreateFormFile("file", p.filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(filePart, bytes.NewReader(p.data)); err != nil {
		return "", fmt.Errorf("copy audio: %w", err)
	}

	if backend.ForwardModel != "" {
		writer.WriteField("model", backend.ForwardModel) //nolint:errcheck
	}
	if language != "" {
		writer.WriteField("language", language) //nolint:errcheck
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
