package core

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/DictionLabs/Diction/gateway/core/transcript"
)

type liveTranscribeFunc func(ctx context.Context, wav []byte) (string, error)

type liveEngine struct {
	cfg        LiveConfig
	final      liveTranscribeFunc
	partial    liveTranscribeFunc
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	workerDone chan struct{}
	closeOnce  sync.Once

	mu        sync.Mutex
	det       pauseDetector
	pcm       []byte
	base      int64
	pending   []liveCut
	texts     []string
	committed int64
	failed    error
	finishing bool
	busy      bool
	lang      string

	partialCancel  context.CancelFunc
	partialFrom    int64
	partialAt      int64
	interims       chan string
	interimsClosed bool

	segments int
}

func newLiveEngine(parent context.Context, cfg LiveConfig, final, partial liveTranscribeFunc) *liveEngine {
	ctx, cancel := context.WithCancel(parent)
	e := &liveEngine{
		cfg: cfg, final: final, partial: partial,
		ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), workerDone: make(chan struct{}),
		interims: make(chan string, 1),
		det:      pauseDetector{cfg: cfg},
	}
	if partial == nil {
		e.interimsClosed = true
		close(e.interims)
	}
	go e.worker()
	return e
}

func (e *liveEngine) Write(p []byte) (int, error) {
	e.mu.Lock()
	if !e.finishing && e.failed == nil {
		e.accept(p)
	}
	e.mu.Unlock()
	e.poke()
	return len(p), nil
}

func (e *liveEngine) accept(p []byte) {
	e.pcm = append(e.pcm, p...)
	e.pending = append(e.pending, e.det.push(p)...)
	if (e.audioEnd()-e.committed)*1000/liveSampleRate <= liveMaxOpenMs {
		return
	}
	if len(e.pending) == 0 {
		e.fail(errLiveNoPause)
	} else {
		e.fail(errLiveBacklog)
	}
}

var (
	errLiveNoPause = errors.New("live: no pause to cut at")
	errLiveBacklog = errors.New("live: backend too far behind")
)

func (e *liveEngine) fail(err error) {
	e.failed = err
	e.pcm, e.pending = nil, nil
}

func (e *liveEngine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *liveEngine) audioEnd() int64 { return e.base + int64(len(e.pcm)/2) }

func (e *liveEngine) wav(from, to int64) []byte {
	a := (from - e.base) * 2
	b := (to - e.base) * 2
	var buf bytes.Buffer
	buf.Grow(int(b-a) + 44)
	_ = WriteWAVHeader(&buf, int(b-a))
	buf.Write(e.pcm[a:b])
	return buf.Bytes()
}

func (e *liveEngine) commit(to int64, text string) {
	if t := strings.TrimSpace(text); t != "" {
		e.texts = append(e.texts, t)
	}
	drop := (to - e.base) * 2
	e.pcm = append([]byte(nil), e.pcm[drop:]...)
	e.base = to
	e.committed = to
}

func (e *liveEngine) committedText() string { return joinSegments(e.texts, e.lang) }

func (e *liveEngine) emit(text string) {
	if e.interimsClosed || text == "" {
		return
	}
	select {
	case e.interims <- text:
	default:
		select {
		case <-e.interims:
		default:
		}
		e.interims <- text
	}
}

func (e *liveEngine) worker() {
	defer close(e.workerDone)
	for {
		select {
		case <-e.wake:
		case <-e.ctx.Done():
			return
		}
		for e.step() {
		}
		e.mu.Lock()
		stop := e.finishing || e.failed != nil
		e.mu.Unlock()
		if stop {
			return
		}
	}
}

func (e *liveEngine) step() bool {
	e.mu.Lock()
	if e.finishing || e.failed != nil || e.ctx.Err() != nil {
		e.mu.Unlock()
		return false
	}
	if len(e.pending) > 0 {
		return e.transcribePending()
	}
	return e.transcribePartial()
}

func (e *liveEngine) transcribePending() bool {
	last := e.pending[len(e.pending)-1]
	e.pending = nil
	from := e.committed
	wav := e.wav(from, last.at)
	e.busy = true
	e.mu.Unlock()

	text, err := e.final(e.ctx, wav)
	if err == nil && implausibleText(text, wav) {
		err = transcript.ErrSTTHallucination
	}
	return e.commitFinal(last.at, text, err)
}

func (e *liveEngine) commitFinal(at int64, text string, err error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.busy = false
	if e.failed != nil {
		return false
	}
	if err != nil {
		e.fail(err)
		return false
	}
	e.commit(at, text)
	e.segments++
	e.emit(e.committedText())
	return true
}

func (e *liveEngine) partialDue() bool {
	end := e.audioEnd()
	grown := end - e.partialAt
	if e.partialFrom != e.committed {
		grown = end - e.committed
	}
	return grown*1000/liveSampleRate >= int64(e.cfg.PartialEveryMs) && e.det.voiced*liveFrameMs >= liveMinVoicedMs
}

func (e *liveEngine) transcribePartial() bool {
	if e.partial == nil || e.interimsClosed || !e.partialDue() {
		e.mu.Unlock()
		return false
	}
	if slots := e.cfg.partialSlots; slots != nil {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			e.mu.Unlock()
			return false
		}
	}
	end := e.audioEnd()
	from := e.committed
	wav := e.wav(from, end)
	pctx, pcancel := context.WithCancel(e.ctx)
	e.partialCancel = pcancel
	e.partialFrom, e.partialAt = from, end
	e.mu.Unlock()

	text, err := e.partial(pctx, wav)
	pcancel()

	e.mu.Lock()
	e.partialCancel = nil
	if err == nil && e.committed == from && !implausibleText(text, wav) {
		joined := joinSegments(append(append([]string(nil), e.texts...), text), e.lang)
		e.emit(joined)
	}
	e.mu.Unlock()
	return true
}

func (e *liveEngine) stopInterims() {
	e.mu.Lock()
	if !e.interimsClosed {
		e.interimsClosed = true
		close(e.interims)
	}
	if e.partialCancel != nil {
		e.partialCancel()
	}
	e.mu.Unlock()
}

type liveFinish struct {
	segments int
	tailSec  float64
	invented bool
}

func (e *liveEngine) Finish(ctx context.Context) (string, bool, liveFinish) {
	var st liveFinish
	e.mu.Lock()
	e.finishing = true
	if e.partialCancel != nil {
		e.partialCancel()
	}
	budget := 5*time.Second + time.Duration(e.audioEnd()-e.committed)*time.Second/liveSampleRate/2
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	e.poke()
	select {
	case <-e.workerDone:
	case <-ctx.Done():
		return "", false, st
	}

	e.mu.Lock()
	st.segments = e.segments
	if e.failed != nil {
		e.mu.Unlock()
		return "", false, st
	}
	tailActive := e.det.active
	for _, c := range e.pending {
		tailActive += c.active
	}
	end := e.audioEnd()
	st.tailSec = float64(end-e.committed) / liveSampleRate
	var wav []byte
	if tailActive*liveFrameMs >= liveTailActiveMs && end > e.committed {
		wav = e.wav(e.committed, end)
	}
	texts := append([]string(nil), e.texts...)
	e.mu.Unlock()

	if wav != nil {
		text, err := e.final(ctx, wav)
		if err != nil || implausibleText(text, wav) {
			st.invented = err == nil && st.segments == 0 && !transcript.HasDegenerateRepetition(text)
			return "", false, st
		}
		if t := strings.TrimSpace(text); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		return "", false, st
	}
	return joinSegments(texts, e.lang), true, st
}

func (e *liveEngine) Close() {
	e.closeOnce.Do(func() {
		e.cancel()
		<-e.workerDone
		e.stopInterims()
	})
}
