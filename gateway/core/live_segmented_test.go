package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func segmentedConfig() LiveConfig {
	c := testLiveConfig()
	c.Segmented = true
	return c
}

// The engine outlives its interims: the segmented final still works after them.
func TestLiveEngine_FinishAfterInterims(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, asr.transcribe)
	defer e.Close()
	go func() {
		for range e.interims {
		}
	}()
	feedLive(t, e, pcmPattern(4000, 1000, 3000))
	e.stopInterims()
	if _, ok, _ := e.Finish(context.Background()); !ok {
		t.Fatal("Finish after interims not ok")
	}
}

func TestPauseDetector_FirstPieceMinimum(t *testing.T) {
	cfg := segmentedConfig() // MinSegMs 3000
	cfg.FirstMinSegMs = 6000
	d := pauseDetector{cfg: cfg}
	// 4 s speech + pause: enough for MinSegMs, not for the first piece's 6 s.
	if cuts := d.push(pcmPattern(4000, 1000)); len(cuts) != 0 {
		t.Fatalf("first piece cut before FirstMinSegMs: %+v", cuts)
	}
	// 2 s more speech + pause: past 6 s, so the first cut comes.
	if cuts := d.push(pcmPattern(2000, 1000)); len(cuts) != 1 {
		t.Fatalf("want the first cut, got %+v", cuts)
	}
	// Later pieces use MinSegMs again: 3.5 s speech + pause cuts.
	if cuts := d.push(pcmPattern(3500, 1000)); len(cuts) != 1 {
		t.Fatalf("want a 3 s-minimum cut after the first piece, got %+v", cuts)
	}
}

func TestLiveEngine_SegmentsInBackgroundAndJoins(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 4000, 1000, 2000))
	text, ok, st := e.Finish(context.Background())
	if !ok {
		t.Fatal("Finish not ok")
	}
	if text != "part1. part2. part3." {
		t.Fatalf("text = %q", text)
	}
	d := asr.durations()
	if len(d) != 3 || st.segments != 2 {
		t.Fatalf("calls %v, segments %d", d, st.segments)
	}
	// The tail (last request) is short: only the audio after the second pause.
	if d[2] > 3.0 {
		t.Fatalf("tail was %.2fs, want < 3 s", d[2])
	}
	if total := d[0] + d[1] + d[2]; math.Abs(total-12.0) > 0.05 {
		t.Fatalf("segments cover %.2fs of 12 s", total)
	}
}

func TestLiveEngine_SilentTailIsSkipped(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1500))
	text, ok, _ := e.Finish(context.Background())
	if !ok || text != "part1." {
		t.Fatalf("got %q ok=%v", text, ok)
	}
	if n := len(asr.durations()); n != 1 {
		t.Fatalf("want 1 request (no tail), got %d", n)
	}
}

func TestLiveEngine_SegmentFailureMeansFallback(t *testing.T) {
	asr := &fakeASR{fail: func(call int) error {
		if call == 0 {
			return errors.New("backend returned 500: secret words")
		}
		return nil
	}}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("Finish ok after a failed segment; want fallback")
	}
}

func TestLiveEngine_TailFailureMeansFallback(t *testing.T) {
	asr := &fakeASR{fail: func(call int) error {
		if call == 1 {
			return errors.New("boom")
		}
		return nil
	}}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("Finish ok after a failed tail; want fallback")
	}
}

func TestLiveEngine_NoSpeechMeansFallback(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(0, 3000))
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("Finish ok with no speech; want the whole clip to decide")
	}
}

func TestLiveEngine_WaitsForInFlightSegment(t *testing.T) {
	asr := &fakeASR{delay: 150 * time.Millisecond}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	text, ok, _ := e.Finish(context.Background())
	if !ok || text != "part1. part2." {
		t.Fatalf("got %q ok=%v", text, ok)
	}
}

func TestStreaming_SegmentedFinal_PCM(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, segmentedConfig())
	frames := streamPCM(t, srv, "", pcmPattern(4000, 1000, 4000, 1000, 2000), 0)
	if got := lastResultText(t, frames); got != "r1 r2 r3" {
		t.Fatalf("final %q, want the three parts joined", got)
	}
	if n := atomic.LoadInt32(calls); n != 3 {
		t.Fatalf("backend calls = %d, want 3 (no whole-clip request)", n)
	}
}

func TestStreaming_SegmentedFinal_FallsBackToWholeClip(t *testing.T) {
	backend, calls, _ := segmentBackend(t, func(n int) bool { return n == 1 })
	srv := liveGateway(t, backend.URL, segmentedConfig())
	frames := streamPCM(t, srv, "", pcmPattern(4000, 1000, 2000), 0)
	// Call 1 (segment) failed, call 2 is the whole clip.
	if got := lastResultText(t, frames); got != "r2" {
		t.Fatalf("final %q, want the whole-clip answer", got)
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("backend calls = %d, want 2", n)
	}
}

func TestStreaming_SegmentedFinal_OffByDefault(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, LiveConfig{})
	frames := streamPCM(t, srv, "", pcmPattern(4000, 1000, 4000, 1000, 2000), 0)
	if got := lastResultText(t, frames); got != "r1" {
		t.Fatalf("final %q", got)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("backend calls = %d, want 1", n)
	}
}

func TestStreaming_SegmentedFinal_Opus(t *testing.T) {
	ogg := testOgg(t, 10) // two pauses to cut at
	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, segmentedConfig())
	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatal(err)
	}
	n := atomic.LoadInt32(calls)
	if n < 2 {
		t.Fatalf("backend calls = %d, want segments", n)
	}
	if want := fmt.Sprintf("r%d", n); !strings.HasSuffix(text, want) || strings.Count(text, "r") != int(n) {
		t.Fatalf("final %q after %d calls", text, n)
	}
	if name, _ := lastName.Load().(string); name != "audio.wav" {
		t.Fatalf("backend got %q, want decoded audio.wav", name)
	}
}

func TestLiveEngine_ShortLastWordIsSent(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	// A 100 ms word after the last pause ("No.") must still be transcribed.
	feedLive(t, e, pcmPattern(4000, 1000, 100, 300))
	text, ok, _ := e.Finish(context.Background())
	if !ok || text != "part1. part2." {
		t.Fatalf("got %q ok=%v", text, ok)
	}
}

func TestLiveEngine_NoPausesMeansFallback(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(liveMaxOpenMs+2000))
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("Finish ok after 92 s without a pause; want the whole clip")
	}
}

func TestLiveEngine_FinishHasABudget(t *testing.T) {
	asr := &fakeASR{delay: time.Hour}
	e := newLiveEngine(context.Background(), segmentedConfig(), asr.transcribe, nil)
	defer e.Close()
	feedChunksNoWait(e, pcmPattern(4000, 1000, 2000))
	start := time.Now()
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("ok with a stalled backend")
	}
	if d := time.Since(start); d > 12*time.Second {
		t.Fatalf("Finish took %v with a stalled backend", d)
	}
}

func TestStreaming_SegmentedFinal_OpusFallbackSendsOriginal(t *testing.T) {
	ogg := testOgg(t, 10) // two pauses to cut at
	// Every segment fails, so the whole clip goes: it must be the original Ogg, as today.
	backend, _, lastName := segmentBackend(t, func(n int) bool { return n == 1 })
	srv := liveGateway(t, backend.URL, segmentedConfig())
	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := lastName.Load().(string); name != "audio.ogg" || text == "" {
		t.Fatalf("fallback sent %q (text %q), want the original audio.ogg", name, text)
	}
}

func TestLiveEngine_InventedTextMeansFallback(t *testing.T) {
	// A backend that appends a long list (its prompt) to a 2 s tail.
	echo := func(ctx context.Context, wav []byte) (string, error) {
		if len(wav) < 4*2*liveSampleRate {
			return "Copy the team, " + strings.Repeat("Some Term, ", 30), nil
		}
		return "part.", nil
	}
	e := newLiveEngine(context.Background(), segmentedConfig(), echo, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	if _, ok, _ := e.Finish(context.Background()); ok {
		t.Fatal("Finish ok with 330 characters from 2 s of audio; want the whole clip")
	}
}
