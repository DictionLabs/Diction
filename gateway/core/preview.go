package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/coder/websocket"
)

// Cloud preview on the batch socket (diction-routes 01b, contract C2).
//
// A client that sends `?preview=cloud` on /v1/audio/stream gets interim text while
// it records: `{"type":"interim","text":"…"}` frames, each the whole running
// transcript so far (replace, don't append). The final answer is untouched — the
// interims come from a separate streaming upstream, never from the batch STT that
// produces the result, and none is ever written after `done`.
//
// The upstream is the private gateway's choice (PreviewRelay); the community build
// leaves the hook nil and `preview=cloud` is ignored. PCM sessions only: an Opus
// session has no PCM until ffmpeg drains after `done`, so it gets no relay.

// PreviewSession is one interim relay. Implementations must make Send non-blocking
// (drop rather than wait) and must close the Interims channel once Stop has been
// called or the relay has ended on its own.
type PreviewSession interface {
	// Send hands one PCM frame (16-bit LE mono 16 kHz) to the relay. Never blocks.
	Send(pcm []byte)
	// Interims yields the running transcript. Closed when the relay ends.
	Interims() <-chan string
	// Stop ends the relay. Interims is closed promptly after it returns.
	Stop()
}

// PreviewRelay opens a relay for one batch-socket session. It must return
// immediately — any upstream dial happens inside the session. Nil (community
// build) or a nil return disables the preview for that session.
var PreviewRelay func(ctx context.Context, language string) PreviewSession

// OnPreviewRelay records a relay outcome the handler itself decided (today only
// "failed" for an Opus session that asked for a preview). Nil by default.
var OnPreviewRelay func(ctx context.Context, outcome string)

// previewInterimWriteTimeout bounds one interim write. A write that cannot finish in
// 100 ms means the client is not reading; the interim is a draft, so it is dropped.
const previewInterimWriteTimeout = 100 * time.Millisecond

type interimFrame struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// previewPump owns one session's relay inside the batch handler: the session and
// the single writer goroutine that turns interims into frames. coder/websocket
// allows Write concurrently with Read, so no lock is needed; ordering against the
// final frames comes from stop(), which joins the writer before the at-stop path.
type previewPump struct {
	sess       PreviewSession
	writerDone chan struct{}
	stopped    bool
}

// startPreviewPump opens the relay and its writer. Returns nil when no relay is
// available; every method is nil-safe.
func startPreviewPump(ctx context.Context, conn *websocket.Conn, language string) *previewPump {
	if PreviewRelay == nil {
		return nil
	}
	sess := PreviewRelay(ctx, language)
	if sess == nil {
		return nil
	}
	p := &previewPump{sess: sess, writerDone: make(chan struct{})}
	go func() {
		defer close(p.writerDone)
		writeFailed := false
		for text := range sess.Interims() {
			if writeFailed {
				continue // keep draining so the relay never blocks on us
			}
			payload, err := json.Marshal(interimFrame{Type: "interim", Text: text})
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, previewInterimWriteTimeout)
			err = conn.Write(wctx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				log.Printf("ws preview: interim write: %v", err)
				writeFailed = true
			}
		}
	}()
	return p
}

// send forwards one PCM frame to the relay.
func (p *previewPump) send(pcm []byte) {
	if p != nil && !p.stopped {
		p.sess.Send(pcm)
	}
}

// stop ends the relay and waits for the writer, so no interim can follow. Idempotent.
func (p *previewPump) stop() {
	if p == nil || p.stopped {
		return
	}
	p.stopped = true
	p.sess.Stop()
	<-p.writerDone
}

// reportPreviewRelay is the nil-safe call for OnPreviewRelay.
func reportPreviewRelay(ctx context.Context, outcome string) {
	if OnPreviewRelay != nil {
		OnPreviewRelay(ctx, outcome)
	}
}
