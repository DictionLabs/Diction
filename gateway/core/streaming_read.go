package core

import (
	"bytes"
	"encoding/json"
	"log"
	"time"

	"github.com/coder/websocket"
)

type frameVerdict uint8

const (
	frameNext frameVerdict = iota
	frameStop
	frameAbort
)

func (s *streamSession) readFrame() (websocket.MessageType, []byte, bool) {
	idleFired := make(chan struct{})
	idleTimer := time.AfterFunc(max(time.Until(s.idleDeadline), 0), func() {
		CloseWSWithTimeout(s.conn, websocket.StatusPolicyViolation, "idle_timeout", 2*time.Second)
		close(idleFired)
	})
	msgType, data, err := s.conn.Read(s.ctx)
	stopped := idleTimer.Stop()
	if !stopped {
		<-idleFired
	}
	if err != nil {
		s.endRead(err, stopped)
		return 0, nil, false
	}
	if !stopped {
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_read",
			Reason:   wsReasonIdleTimeout,
			Endpoint: routeAudioStream,
			Hint:     "websocket read failed: " + wsReasonIdleTimeout,
		}, errTypeClientAbort, 0, "")
		return 0, nil, false
	}
	return msgType, data, true
}

func (s *streamSession) endRead(err error, stopped bool) {
	log.Printf("ws read: %v", err)
	var reason string
	if !stopped {
		reason = wsReasonIdleTimeout
	} else {
		reason = ClassifyWSError(err)
	}
	var code websocket.StatusCode
	if stopped {
		code = websocket.StatusInternalError
	}
	s.out.end(s.ctx, &ErrorEvent{
		Source:   sourceStreaming,
		Kind:     "ws_read",
		Reason:   reason,
		Endpoint: routeAudioStream,
		Hint:     "websocket read failed: " + reason,
	}, errTypeClientAbort, code, reason)
}

func (s *streamSession) readLoop() bool {
	for {
		msgType, data, ok := s.readFrame()
		if !ok {
			return false
		}
		if msgType == websocket.MessageBinary {
			switch s.handleAudio(data) {
			case frameAbort:
				return false
			case frameStop:
				return true
			}
			s.phase = readAudio
			continue
		}
		if msgType == websocket.MessageText && s.handleText(data) {
			return true
		}
	}
}

func (s *streamSession) handleText(data []byte) bool {
	var action streamAction
	if err := json.Unmarshal(data, &action); err != nil {
		if s.phase == readAwaitingContext {
			s.contextJSON = string(data)
			s.phase = readContextSeen
		}
		return false
	}
	if action.Action == "done" {
		if action.Language != "" && !s.detectActive {
			s.language = action.Language
		}
		return true
	}
	if s.phase == readAwaitingContext && action.Action == "" {
		s.contextJSON = string(data)
		s.phase = readContextSeen
	}
	return false
}

func (s *streamSession) handleAudio(data []byte) frameVerdict {
	s.idleDeadline = time.Now().Add(s.idleTimeout)
	s.framesRx++
	s.audioBytesRx += int64(len(data))
	if s.audioBytesRx > s.maxPCM {
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_audio_too_large",
			Reason:   "compressed",
			Endpoint: routeAudioStream,
			Hint:     "received audio exceeded maxPCM before decode",
		}, errTypeSTTError, wsCloseTooLarge, "audio too large")
		return frameAbort
	}

	if s.liveOn && s.live == nil {
		s.liveLanguage = s.language
		s.live = s.g.startLive(s.ctx, s.target, s.backend, s.language, s.upstreamLanguageFor(s.language), s.contextJSON, s.builtinPreview)
		if s.builtinPreview {
			s.preview = startPreviewPumpWith(s.ctx, s.conn, livePreview{s.live})
		}
	}

	if s.isOpus && !s.containerSniffed && !s.startOpusDecode(data) {
		return frameAbort
	}
	return s.routeAudio(data)
}

func (s *streamSession) routeAudio(data []byte) frameVerdict {
	switch {
	case s.usePassthrough:
		s.opusInputBuf.Write(data)
	case s.keepOriginal:
		s.opusInputBuf.Write(data)
		if s.ffmpegStdin != nil && !s.liveBroken {
			if _, werr := s.ffmpegStdin.Write(data); werr != nil {
				log.Printf("live: decoder stopped mid-stream; live off for this session")
				s.liveBroken = true
			}
		}
	case s.isOpus:
		if _, werr := s.ffmpegStdin.Write(data); werr != nil {
			s.ffmpegWriteErr = werr
			return frameStop
		}
	default:
		s.pcmBuf.Write(data)
		if s.live != nil {
			_, _ = s.live.Write(data)
		}
		if s.wantPreview && s.preview == nil && len(data) > 0 {
			s.preview = startPreviewPump(s.ctx, s.conn, s.language)
			s.wantPreview = false
		}
		s.preview.send(data)
	}
	return frameNext
}

var (
	oggMagic  = []byte("OggS")
	webmMagic = []byte("\x1a\x45\xdf\xa3")
)

func (s *streamSession) sniffContainer(data []byte) bool {
	s.containerSniffed = true
	switch {
	case bytes.HasPrefix(data, oggMagic):
		s.containerFile = fileNameOgg
	case bytes.HasPrefix(data, webmMagic):
		s.containerFile = "audio.webm"
	default:
		log.Printf("ws: codec=opus but first frame is neither Ogg nor WebM")
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     kindOpusDecode,
			Endpoint: routeAudioStream,
			Hint:     "expected Ogg or WebM container; got unknown magic bytes",
		}, errTypeSTTError, wsCloseUnknown, "expected Ogg or WebM container")
		return false
	}
	return true
}
