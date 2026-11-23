package core

import (
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

func (s *streamSession) startOpusDecode(data []byte) bool {
	if !s.sniffContainer(data) {
		return false
	}
	if s.keepOriginal {
		s.startLiveDecode()
		return true
	}
	if s.usePassthrough {
		return true
	}
	if !s.acquireDecodeSlot() {
		return false
	}
	return s.startFFmpegDecoder()
}

func (s *streamSession) startLiveDecode() {
	releaseLive, ok := tryLiveDecodeSlot()
	if !ok {
		log.Printf("live: decode slots busy; live off for this session")
		s.liveBroken = true
		return
	}
	releaseCaller, ok := opusPrincipals.TryAcquire(s.principal, opusMaxPerPrincipal())
	if !ok {
		releaseLive()
		log.Printf("live: caller's decode slots busy; live off for this session")
		s.liveBroken = true
		return
	}
	var once sync.Once
	s.releaseDecodeSlot = func() { once.Do(func() { releaseLive(); releaseCaller() }) }
	inputFmt := strings.TrimPrefix(s.containerFile, "audio.")
	cmd, stdin, done, derr := startLiveDecoder(s.ctx, inputFmt, &s.ffmpegStderr, s.live, &s.liveDecodedBytes)
	if derr != nil {
		log.Printf("live: decoder did not start; live off for this session")
		s.liveBroken = true
		return
	}
	s.ffmpegCmd, s.ffmpegStdin, s.stdoutDone = cmd, stdin, done
}

func (s *streamSession) acquireDecodeSlot() bool {
	releasePrincipal, ok := opusPrincipals.TryAcquire(s.principal, opusMaxPerPrincipal())
	if !ok {
		log.Printf("ws: Opus per-caller cap reached; rejecting connection")
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_busy",
			Reason:   "principal_cap",
			Endpoint: routeAudioStream,
			Hint:     "caller already holds the maximum Opus decode slots",
		}, errTypeSTTError, wsCloseFailed, "server busy; too many concurrent Opus decodes")
		return false
	}
	sem := getOpusSem()
	waitTimer := time.NewTimer(5 * time.Second)
	defer waitTimer.Stop()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		s.releaseDecodeSlot = func() { once.Do(func() { <-sem; releasePrincipal() }) }
		return true
	case <-waitTimer.C:
		releasePrincipal()
		log.Printf("ws: Opus semaphore saturated; rejecting connection")
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_busy",
			Endpoint: routeAudioStream,
			Hint:     "opus decode semaphore saturated after 5s",
		}, errTypeSTTError, wsCloseFailed, "server busy; too many concurrent Opus decodes")
		return false
	case <-s.ctx.Done():
		releasePrincipal()
		return false
	}
}

func (s *streamSession) ffmpegFailure(hint string) bool {
	s.out.end(s.ctx, &ErrorEvent{
		Source:   sourceStreaming,
		Kind:     kindOpusDecode,
		Endpoint: routeAudioStream,
		Hint:     hint,
	}, errTypeSTTError, websocket.StatusInternalError, "ffmpeg error")
	return false
}

func (s *streamSession) startFFmpegDecoder() bool {
	inputFmt := strings.TrimPrefix(s.containerFile, "audio.")
	cmd := ffmpegCommand(s.ctx, lookupFfmpeg(),
		"-loglevel", "error",
		"-f", inputFmt, "-i", "pipe:0",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stderr = &s.ffmpegStderr

	stdin, serr := cmd.StdinPipe()
	if serr != nil {
		log.Printf("ws ffmpeg stdin pipe: %v", serr)
		return s.ffmpegFailure("ffmpeg stdin pipe failed")
	}
	stdout, serr := cmd.StdoutPipe()
	if serr != nil {
		log.Printf("ws ffmpeg stdout pipe: %v", serr)
		return s.ffmpegFailure("ffmpeg stdout pipe failed")
	}
	if serr = cmd.Start(); serr != nil {
		log.Printf("ws ffmpeg start: %v", serr)
		return s.ffmpegFailure("ffmpeg start failed")
	}
	s.ffmpegCmd = cmd
	s.ffmpegStdin = stdin
	done := make(chan struct{})
	s.stdoutDone = done
	var sink io.Writer = &s.pcmBuf
	if s.live != nil {
		sink = io.MultiWriter(&s.pcmBuf, s.live)
	}
	maxPCM := s.maxPCM
	go func() {
		_, _ = io.Copy(sink, io.LimitReader(stdout, maxPCM+1))
		close(done)
	}()
	return true
}

func (s *streamSession) ffmpegCleanup() {
	if s.ffmpegCleaned || s.ffmpegStdin == nil {
		return
	}
	s.ffmpegCleaned = true
	_ = s.ffmpegStdin.Close()
	if s.stdoutDone != nil {
		<-s.stdoutDone
	}
	s.ffmpegWaitErr = s.ffmpegCmd.Wait()
	if s.releaseDecodeSlot != nil {
		s.releaseDecodeSlot()
	}
}
