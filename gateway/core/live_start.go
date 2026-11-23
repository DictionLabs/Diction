package core

import (
	"context"
	"io"
	"log"
	"net/url"
	"os/exec"
	"sync"
	"sync/atomic"
)

type livePreview struct{ e *liveEngine }

func (p livePreview) Send([]byte)             {}
func (p livePreview) Interims() <-chan string { return p.e.interims }
func (p livePreview) Stop()                   { p.e.stopInterims() }

func (g *Gateway) PreviewCapabilities() (pcm, opus bool) {
	if PreviewRelay != nil {
		return true, false
	}
	if !g.live.Preview {
		return false, false
	}
	return true, lookupFfmpeg() != ""
}

func (g *Gateway) startLive(
	ctx context.Context, target *url.URL, backend *Backend,
	language, upstreamLanguage, contextJSON string, preview bool,
) *liveEngine {
	prompt := buildWhisperPrompt(contextJSON)
	customWords := buildCustomWordsField(contextJSON)
	final := func(ctx context.Context, wav []byte) (string, error) {
		return g.proxyToBackend(ctx, target, audioPayload{data: wav, filename: fileNameWav}, backend, upstreamLanguage, prompt, customWords)
	}
	var partial liveTranscribeFunc
	if preview {
		partial = g.previewTranscriber(final, upstreamLanguage)
	}
	e := newLiveEngine(ctx, g.live, final, partial)
	e.lang = language
	return e
}

func (g *Gateway) previewTranscriber(final liveTranscribeFunc, upstreamLanguage string) liveTranscribeFunc {
	if g.live.PreviewURL == "" {
		return final
	}
	pu, err := url.Parse(g.live.PreviewURL)
	if err != nil || pu.Host == "" {
		log.Printf("live: DICTION_PREVIEW_URL is not a URL; partials use the session backend")
		return final
	}
	pb := &Backend{Name: "preview", ForwardModel: g.live.PreviewModel, AuthHeader: g.live.PreviewAuth}
	return func(ctx context.Context, wav []byte) (string, error) {
		return g.proxyToBackend(ctx, pu, audioPayload{data: wav, filename: fileNameWav}, pb, upstreamLanguage, "", "")
	}
}

func (e *liveEngine) close() {
	if e != nil {
		e.Close()
	}
}

var (
	liveDecodeOnce  sync.Once
	liveDecodeSlots chan struct{}
)

func tryLiveDecodeSlot() (release func(), ok bool) {
	liveDecodeOnce.Do(func() { liveDecodeSlots = make(chan struct{}, cap(getOpusSem())) })
	select {
	case liveDecodeSlots <- struct{}{}:
		return func() { <-liveDecodeSlots }, true
	default:
		return nil, false
	}
}

func startLiveDecoder(
	ctx context.Context, inputFmt string, stderr io.Writer, live *liveEngine, decoded *atomic.Int64,
) (*exec.Cmd, io.WriteCloser, chan struct{}, error) {
	cmd := ffmpegCommand(ctx, lookupFfmpeg(), "-loglevel", "error",
		"-f", inputFmt, "-i", "pipe:0", "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(countingWriter{live, decoded}, stdout)
	}()
	return cmd, stdin, done, nil
}

type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return c.w.Write(p)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
