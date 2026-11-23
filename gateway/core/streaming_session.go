package core

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type streamSession struct {
	g            *Gateway
	w            http.ResponseWriter
	r            *http.Request
	query        url.Values
	inline       func(context.Context, string, string, string) (string, string, error)
	postDelivery func(context.Context, string, string, string) (string, string, error)

	out    streamOutcome
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc

	codec                 string
	language              string
	model                 string
	stripUpstreamLanguage bool
	detectActive          bool
	adResult              AutoDetectResult
	adCtx                 AutoDetectContext
	target                *url.URL
	backend               *Backend

	ffmpegAvail       bool
	canOfferOpus      bool
	clientOfferedOpus bool
	socketOpenedAt    time.Time
	isOpus            bool
	previewParam      bool
	builtinPreview    bool
	liveOn            bool
	keepOriginal      bool
	usePassthrough    bool
	principal         string

	releaseDecodeSlot func()

	pcmBuf           bytes.Buffer
	opusInputBuf     bytes.Buffer
	contextJSON      string
	maxPCM           int64
	phase            streamReadPhase
	audioBytesRx     int64
	framesRx         int
	containerFile    string
	containerSniffed bool

	ffmpegCmd      *exec.Cmd
	ffmpegStdin    io.WriteCloser
	ffmpegStderr   limitedStderrWriter
	stdoutDone     chan struct{}
	ffmpegWriteErr error
	ffmpegCleaned  bool
	ffmpegWaitErr  error

	liveBroken       bool
	liveDecodedBytes atomic.Int64
	live             *liveEngine
	liveLanguage     string
	preview          *previewPump
	wantPreview      bool

	idleTimeout  time.Duration
	idleDeadline time.Time

	phoneTranscripts *deviceTranscripts
	upstreamLang     string
	payload          audioPayload
	audioDurationMs  int64
	whisperPrompt    string
	customWordsJSON  string
	text             string
	sttMs            int64
	sttDoneAt        time.Time

	enhanceEnabled bool
	intentParam    string
	isEdit         bool
	enhanceCtxJSON string
	autoEdit       bool
}

func (g *Gateway) newStreamSession(
	w http.ResponseWriter, r *http.Request,
	inline, postDelivery func(context.Context, string, string, string) (string, string, error),
) *streamSession {
	return &streamSession{
		g: g, w: w, r: r, query: r.URL.Query(), inline: inline, postDelivery: postDelivery,
		cancel:       func() {},
		ffmpegStderr: limitedStderrWriter{cap: 4096},
		maxPCM:       g.maxBodySize,
		phase:        readAwaitingContext,
	}
}

func (s *streamSession) serve() {
	s.codec = s.query.Get("codec")
	switch s.codec {
	case "", "pcm", codecOpus:
	default:
		http.Error(s.w, `{"error":"unsupported codec; accepted values: pcm, opus"}`, http.StatusBadRequest)
		return
	}

	defer s.finish()
	defer s.releaseSlot()
	defer s.ffmpegCleanup()
	defer func() { s.preview.stop() }()
	defer func() { s.live.close() }()

	if !s.route() || !s.accept() || !s.negotiateCodec() {
		return
	}
	s.planPipeline()
	s.prepareRead()
	if !s.readLoop() {
		return
	}
	s.deliver()
}

func (s *streamSession) finish() {
	s.out.emit(s.w, s.conn)
	s.cancel()
	if s.conn != nil {
		_ = s.conn.CloseNow()
	}
}

func (s *streamSession) releaseSlot() {
	if s.releaseDecodeSlot != nil {
		s.releaseDecodeSlot()
	}
}

func (s *streamSession) route() bool {
	s.language = s.query.Get("language")
	s.detectActive = IsAutoDetect(s.language)
	if s.detectActive {
		s.autoDetectModel()
	}
	if s.model == "" {
		s.model = s.g.ModelForLanguage(s.language)
	}
	s.target, s.backend = s.g.resolveBackend(s.model)
	if s.target == nil || !s.g.healthy(s.model) {
		s.w.Header().Set("X-Diction-Route-Model", s.model)
		s.out.endHTTP(s.r.Context(), &ErrorEvent{
			Source:     sourceSTT,
			Kind:       "stt_route_fail",
			Endpoint:   routeAudioStream,
			Provider:   s.model,
			HTTPStatus: http.StatusServiceUnavailable,
			Hint:       "no healthy backend for this language",
		}, errTypeSTTError, http.StatusServiceUnavailable, `{"error":"backend unavailable"}`)
		return false
	}
	if s.g.fallbackModel != "" {
		log.Printf("Route: language=%s detect=%v → model=%s",
			s.language, s.detectActive, s.model)
	}
	s.w.Header().Set("X-Diction-Route-Lang", s.language)
	s.w.Header().Set("X-Diction-Route-Model", s.model)
	if s.detectActive {
		s.w.Header().Set("X-Diction-Route-Detect", valueTrue)
	}
	return true
}

func (s *streamSession) autoDetectModel() {
	if s.g.DeviceHashFromContext != nil {
		s.adCtx.DeviceHash = s.g.DeviceHashFromContext(s.r.Context())
	}
	if s.adCtx.DeviceHash != "" && s.g.profileStore != nil {
		s.adCtx.Profile = s.g.profileStore.GetProfile(s.r.Context(), s.adCtx.DeviceHash)
	}
	s.adResult = s.g.ModelForAutoDetect(s.adCtx)
	if s.adResult.Model != "" {
		s.model = s.adResult.Model
		s.stripUpstreamLanguage = s.adResult.UpstreamLanguage == ""
	}
}

func (s *streamSession) upstreamLanguageFor(lang string) string {
	if s.adResult.UpstreamLanguage != "" {
		return s.adResult.UpstreamLanguage
	}
	if s.stripUpstreamLanguage {
		return ""
	}
	return lang
}

func (s *streamSession) accept() bool {
	s.ffmpegAvail = lookupFfmpeg() != ""
	s.canOfferOpus = s.ffmpegAvail || !s.backend.NeedsWAV
	s.clientOfferedOpus = strings.Contains(s.r.Header.Get("Sec-WebSocket-Protocol"), opusSubprotocol)
	acceptOpts := &websocket.AcceptOptions{
		OriginPatterns: WebSocketOriginPatterns(),
	}
	if s.canOfferOpus {
		acceptOpts.Subprotocols = []string{opusSubprotocol}
	}

	var err error
	s.conn, err = websocket.Accept(s.w, s.r, acceptOpts)
	if err != nil {
		log.Printf("ws accept: %v", err)
		s.out.end(s.r.Context(), &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_accept",
			Endpoint: routeAudioStream,
			Hint:     "websocket accept failed",
		}, errTypeSTTError, 0, "")
		s.conn = nil
		return false
	}

	s.socketOpenedAt = time.Now()
	s.conn.SetReadLimit(maxWSMessageBytes)
	s.ctx, s.cancel = context.WithTimeout(s.r.Context(), streamTimeout)
	return true
}

func (s *streamSession) negotiateCodec() bool {
	negotiatedOpus := s.conn.Subprotocol() == opusSubprotocol
	negotiationOutcome := ""
	if s.codec == codecOpus {
		switch {
		case negotiatedOpus:
			s.isOpus = true
			negotiationOutcome = "accepted"
		case s.clientOfferedOpus:
			log.Printf("ws: client offered %s but server declined; falling back to PCM", opusSubprotocol)
			negotiationOutcome = "unavailable"
		default:
			if !s.ffmpegAvail && s.backend.NeedsWAV {
				log.Printf("ws: codec=opus requested but ffmpeg not in PATH")
				s.out.end(s.ctx, &ErrorEvent{
					Source:   sourceStreaming,
					Kind:     kindOpusDecode,
					Endpoint: routeAudioStream,
					Hint:     "ffmpeg not found; cannot decode Opus for this backend",
				}, errTypeSTTError, wsCloseFailed, "opus unavailable: ffmpeg not in PATH")
				return false
			}
			s.isOpus = true
		}
	} else if s.canOfferOpus && s.clientOfferedOpus && !negotiatedOpus {
		negotiationOutcome = "offered"
	}

	codecTag := "pcm"
	if s.isOpus {
		codecTag = codecOpus
	}
	if s.g.OnStreamingCodec != nil {
		s.g.OnStreamingCodec(s.ctx, codecTag, negotiationOutcome)
	}
	return true
}

func (s *streamSession) planPipeline() {
	s.previewParam = s.query.Get("preview") == "cloud"
	s.builtinPreview = s.previewParam && PreviewRelay == nil && s.g.live.Preview
	s.liveOn = (s.g.live.Segmented || s.builtinPreview) && (!s.isOpus || s.ffmpegAvail)
	s.keepOriginal = s.isOpus && !s.backend.NeedsWAV && s.liveOn
	s.builtinPreview = s.builtinPreview && s.liveOn
	s.usePassthrough = s.isOpus && !s.backend.NeedsWAV && !s.liveOn

	if StreamPrincipal != nil {
		s.principal = StreamPrincipal(s.r)
	}
}

func (s *streamSession) prepareRead() {
	s.idleTimeout = s.g.streamIdleTimeout
	if s.idleTimeout <= 0 {
		s.idleTimeout = defaultStreamIdleTimeout
	}

	s.wantPreview = s.previewParam && PreviewRelay != nil
	if s.wantPreview && s.isOpus {
		reportPreviewRelay(s.ctx, statusFailed)
		s.wantPreview = false
	}
	s.idleDeadline = time.Now().Add(s.idleTimeout)
}
