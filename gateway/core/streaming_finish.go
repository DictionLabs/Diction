package core

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DictionLabs/Diction/gateway/core/transcript"
)

func (s *streamSession) deliver() {
	s.preview.stop()
	s.startPhoneReader()
	s.ffmpegCleanup()
	s.checkLiveDecode()
	if !s.checkAudio() {
		return
	}
	s.upstreamLang = s.upstreamLanguageFor(s.language)
	if !s.buildPayload() {
		return
	}
	if s.g.OnAudioReceived != nil {
		s.g.OnAudioReceived(s.ctx, s.audioDurationMs)
	}
	if !s.transcribe() {
		return
	}
	s.deliverText()
}

func (s *streamSession) startPhoneReader() {
	if s.query.Get("merge") == "1" && MergeProcess != nil && s.inline != nil &&
		s.query.Get("enhance") == valueTrue && s.query.Get("split_enhance") != valueTrue &&
		!isEditIntentParam(s.query.Get("intent")) {
		s.phoneTranscripts = startDeviceTranscriptReader(s.ctx, s.conn, expectedPhoneTranscripts(s.query.Get("phones")))
	}
}

func (s *streamSession) checkLiveDecode() {
	if !s.keepOriginal || s.liveBroken {
		return
	}
	decodedMs := s.liveDecodedBytes.Load() * 1000 / pcmBytesPerSecond
	oggMs := int64(-1)
	if s.containerFile == fileNameOgg {
		oggMs = oggDurationMs(s.opusInputBuf.Bytes())
	}
	if s.ffmpegWaitErr != nil || (oggMs >= 0 && absInt64(decodedMs-oggMs) > 500) {
		log.Printf("live: decode incomplete (decoded %dms of %dms, decoder_exit_ok=%v, decoder_stderr=%d bytes); using the whole clip",
			decodedMs, oggMs, s.ffmpegWaitErr == nil, s.ffmpegStderr.buf.Len())
		s.liveBroken = true
	}
}

func (s *streamSession) checkAudio() bool {
	if s.ffmpegWriteErr != nil {
		hint := fmt.Sprintf("ffmpeg stdin write error: %v", s.ffmpegWriteErr)
		if msg := strings.TrimSpace(s.ffmpegStderr.buf.String()); msg != "" {
			hint += "; ffmpeg: " + msg
		}
		log.Printf("ws opus decode: %s", hint)
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceSTT,
			Kind:     kindOpusDecode,
			Endpoint: routeAudioStream,
			Hint:     hint,
		}, errTypeSTTError, wsCloseFailed, "opus decode error")
		return false
	}

	if s.isOpus && !s.usePassthrough && int64(s.pcmBuf.Len()) > s.maxPCM {
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_audio_too_large",
			Reason:   "decoded",
			Endpoint: routeAudioStream,
			Hint:     "decoded PCM exceeded maxPCM",
		}, errTypeSTTError, wsCloseTooLarge, "decoded audio too large")
		return false
	}

	if s.pcmBuf.Len() == 0 && s.opusInputBuf.Len() == 0 {
		s.out.end(s.ctx, &ErrorEvent{
			Source:     sourceStreaming,
			Kind:       "ws_no_audio",
			Endpoint:   routeAudioStream,
			Hint:       "socket closed with zero audio bytes",
			LatencyMs:  time.Since(s.socketOpenedAt).Milliseconds(),
			InputChars: s.framesRx,
		}, errTypeNoAudio, wsCloseNoAudio, "no audio received")
		return false
	}
	return true
}

func (s *streamSession) buildPayload() bool {
	if s.usePassthrough || s.keepOriginal {
		s.payload = audioPayload{data: s.opusInputBuf.Bytes(), filename: s.containerFile}
		if s.containerFile == fileNameOgg {
			s.audioDurationMs = oggDurationMs(s.opusInputBuf.Bytes())
		}
		if s.audioDurationMs == 0 && s.opusInputBuf.Len() > 0 {
			log.Printf("ws passthrough: could not derive duration from %s; reporting 0", s.containerFile)
		}
		return true
	}
	var wavBuf bytes.Buffer
	if err := WriteWAVHeader(&wavBuf, s.pcmBuf.Len()); err != nil {
		log.Printf("ws wav header: %v", err)
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceSTT,
			Kind:     "wav_encode",
			Endpoint: routeAudioStream,
			Hint:     "WAV header write failed",
		}, errTypeSTTError, wsCloseFailed, "internal error")
		return false
	}
	wavBuf.Write(s.pcmBuf.Bytes())
	s.payload = audioPayload{data: wavBuf.Bytes(), filename: fileNameWav}
	s.audioDurationMs = int64(s.pcmBuf.Len()) * 1000 / pcmBytesPerSecond
	return true
}

func (s *streamSession) finishLive() bool {
	if s.live == nil || !s.g.live.Segmented || s.liveBroken {
		return false
	}
	if s.language != s.liveLanguage {
		log.Printf("live: language changed at done; using the whole clip")
		return false
	}
	t, ok, st := s.live.Finish(s.ctx)
	segmented := ok
	if ok {
		s.text = t
	}
	if st.invented {
		log.Printf("live: more text than %.2fs of audio can hold; empty result", st.tailSec)
		segmented = true
	}
	return segmented
}

func (s *streamSession) transcribe() bool {
	s.whisperPrompt = buildWhisperPrompt(s.contextJSON)
	s.customWordsJSON = buildCustomWordsField(s.contextJSON)
	sttStart := time.Now()
	var sttErr error
	segmented := s.finishLive()
	s.live.close()
	if !segmented {
		s.text, sttErr = s.g.proxyToBackend(s.ctx, s.target, s.payload, s.backend, s.upstreamLang, s.whisperPrompt, s.customWordsJSON)
		if sttErr == nil && s.g.live.Segmented && s.audioDurationMs > 0 &&
			int64(utf8.RuneCountInString(s.text)) > liveMaxCharsPerSec*s.audioDurationMs/1000+20 && !transcript.HasDegenerateRepetition(s.text) {
			log.Printf("live: %d chars is more text than %dms of audio can hold; failing the dictation",
				utf8.RuneCountInString(s.text), s.audioDurationMs)
			sttErr = transcript.ErrImplausibleLength
		}
	}
	s.sttMs = time.Since(sttStart).Milliseconds()
	if sttErr == nil && transcript.HasDegenerateRepetition(s.text) {
		sttErr = transcript.ErrSTTHallucination
	}
	if sttErr != nil && !s.retryAfterFailure(sttErr) {
		return false
	}
	s.sttDoneAt = time.Now()
	s.ctx = WithSTTBoosted(s.ctx, customWordsFor(s.backend, s.customWordsJSON) != "")
	return true
}

func (s *streamSession) retryAfterFailure(sttErr error) bool {
	log.Printf("ws proxy: %v", sttErr)
	retry := streamRetryInput{
		model: s.model, language: s.language, detectActive: s.detectActive,
		adResult: s.adResult, adCtx: s.adCtx, payload: s.payload,
		usePassthrough: s.usePassthrough || s.keepOriginal, upstreamLanguage: s.upstreamLang,
		whisperPrompt: s.whisperPrompt, customWordsJSON: s.customWordsJSON,
	}
	var retryMs int64
	var ok bool
	if s.text, s.backend, retryMs, ok = s.g.handleStreamBackendFailure(s.ctx, sttErr, s.backend, retry); !ok {
		s.out.end(s.ctx, nil, "", wsCloseFailed, "transcription failed")
		return false
	}
	s.sttMs += retryMs
	return true
}
