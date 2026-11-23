package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/coder/websocket"
)

type PreviewSession interface {
	Send(pcm []byte)
	Interims() <-chan string
	Stop()
}

var PreviewRelay func(ctx context.Context, language string) PreviewSession

var OnPreviewRelay func(ctx context.Context, outcome string)

const previewInterimWriteTimeout = 100 * time.Millisecond

type interimFrame struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type previewPump struct {
	sess       PreviewSession
	writerDone chan struct{}
	stopped    bool
}

func startPreviewPump(ctx context.Context, conn *websocket.Conn, language string) *previewPump {
	if PreviewRelay == nil {
		return nil
	}
	return startPreviewPumpWith(ctx, conn, PreviewRelay(ctx, language))
}

func startPreviewPumpWith(ctx context.Context, conn *websocket.Conn, sess PreviewSession) *previewPump {
	if sess == nil {
		return nil
	}
	p := &previewPump{sess: sess, writerDone: make(chan struct{})}
	go func() {
		defer close(p.writerDone)
		writeFailed := false
		for text := range sess.Interims() {
			if writeFailed {
				continue
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

func (p *previewPump) send(pcm []byte) {
	if p != nil && !p.stopped {
		p.sess.Send(pcm)
	}
}

func (p *previewPump) stop() {
	if p == nil || p.stopped {
		return
	}
	p.stopped = true
	p.sess.Stop()
	<-p.writerDone
}

func reportPreviewRelay(ctx context.Context, outcome string) {
	if OnPreviewRelay != nil {
		OnPreviewRelay(ctx, outcome)
	}
}
