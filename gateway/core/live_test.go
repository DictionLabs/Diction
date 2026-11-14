package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pcmPattern renders alternating speech-like tone and quiet noise. Each entry is a
// duration in ms; even indexes are tone, odd ones are pauses.
func pcmPattern(ms ...int) []byte {
	rng := rand.New(rand.NewSource(1))
	var buf bytes.Buffer
	n := 0
	for i, d := range ms {
		for s := 0; s < d*liveSampleRate/1000; s++ {
			v := rng.NormFloat64() * 32768 * math.Pow(10, -55.0/20) // noise floor
			if i%2 == 0 {
				v += 0.1 * 32768 * math.Sin(2*math.Pi*220*float64(n)/liveSampleRate) // ~-23 dBFS
			}
			n++
			binary.Write(&buf, binary.LittleEndian, int16(v)) //nolint:errcheck
		}
	}
	return buf.Bytes()
}

func testLiveConfig() LiveConfig {
	return LiveConfig{PauseMs: 700, MinSegMs: 3000, MaxSegMs: 20000, ShortPauseMs: 300, PartialEveryMs: 100}
}

// feedLive feeds pcm in 100 ms writes and lets the worker drain after each one, like
// audio arriving in real time (audio that arrives all at once folds pending cuts into
// the tail, which is correct but makes the request count timing-dependent).
func feedLive(t *testing.T, e *liveEngine, pcm []byte) {
	t.Helper()
	for len(pcm) > 0 {
		n := 3200
		if n > len(pcm) {
			n = len(pcm)
		}
		e.Write(pcm[:n]) //nolint:errcheck
		pcm = pcm[n:]
		deadline := time.Now().Add(5 * time.Second)
		for {
			e.mu.Lock()
			idle := len(e.pending) == 0 && !e.busy
			e.mu.Unlock()
			if idle || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func TestPauseDetector_CutsOnlyAtLongPauses(t *testing.T) {
	d := pauseDetector{cfg: testLiveConfig()}
	// 4 s speech, 0.3 s gap (too short), 2 s speech, 1 s gap (cut), 4 s speech.
	pcm := pcmPattern(4000, 300, 2000, 1000, 4000)
	var cuts []liveCut
	for i := 0; i < len(pcm); i += 682 { // odd chunk size, like the app's PCM frames
		end := i + 682
		if end > len(pcm) {
			end = len(pcm)
		}
		cuts = append(cuts, d.push(pcm[i:end])...)
	}
	if len(cuts) != 1 {
		t.Fatalf("want 1 cut, got %d: %+v", len(cuts), cuts)
	}
	at := float64(cuts[0].at) / liveSampleRate
	if at < 6.3 || at > 7.3 {
		t.Fatalf("cut at %.2fs, want inside the 6.3-7.3 s pause", at)
	}
}

func TestPauseDetector_RespectsMinSegment(t *testing.T) {
	d := pauseDetector{cfg: testLiveConfig()}
	// 1 s speech then a long pause: shorter than MinSegMs, so no cut yet.
	if cuts := d.push(pcmPattern(1000, 1500)); len(cuts) != 0 {
		t.Fatalf("want no cut, got %+v", cuts)
	}
}

func TestPauseDetector_ShortPauseAfterMaxSegment(t *testing.T) {
	cfg := testLiveConfig()
	cfg.MaxSegMs = 5000
	d := pauseDetector{cfg: cfg}
	// 6 s speech, then a 400 ms pause: past MaxSegMs the 300 ms short pause is enough.
	if cuts := d.push(pcmPattern(6000, 400, 1000)); len(cuts) != 1 {
		t.Fatalf("want 1 cut, got %+v", cuts)
	}
}

type fakeASR struct {
	mu    sync.Mutex
	calls []float64 // seconds of audio per call
	fail  func(call int) error
	delay time.Duration
}

func (f *fakeASR) transcribe(ctx context.Context, wav []byte) (string, error) {
	f.mu.Lock()
	call := len(f.calls)
	f.calls = append(f.calls, float64(len(wav)-44)/2/liveSampleRate)
	fail := f.fail
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if fail != nil {
		if err := fail(call); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("part%d.", call+1), nil
}

func (f *fakeASR) durations() []float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]float64(nil), f.calls...)
}

func TestLiveEngine_PartialsCarryWholeTranscript(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, asr.transcribe)
	defer e.Close()
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for s := range e.interims {
			mu.Lock()
			got = append(got, s)
			mu.Unlock()
		}
	}()
	pcm := pcmPattern(4000, 1000, 3000)
	for i := 0; i < len(pcm); i += 6400 { // 200 ms per write, paced
		end := i + 6400
		if end > len(pcm) {
			end = len(pcm)
		}
		e.Write(pcm[i:end]) //nolint:errcheck
		time.Sleep(5 * time.Millisecond)
	}
	// After the pause the first segment is committed; the next interim is that text
	// plus the open segment's.
	whole := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) > 0 && strings.Count(got[len(got)-1], "part") == 2
	}
	for deadline := time.Now().Add(5 * time.Second); !whole() && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	e.stopInterims()
	<-done
	if !whole() {
		t.Fatalf("last interim does not carry the committed segment and the open one: %q", got)
	}
	for _, s := range got {
		if s == "" {
			t.Fatal("empty interim")
		}
	}
}

// segmentBackend answers every request with a counter; fail(n) can make call n fail.
func segmentBackend(t *testing.T, fail func(n int) bool) (*httptest.Server, *int32, *atomic.Value) {
	t.Helper()
	var n int32
	var lastName atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(atomic.AddInt32(&n, 1))
		r.ParseMultipartForm(50 << 20) //nolint:errcheck
		if _, fh, err := r.FormFile("file"); err == nil {
			lastName.Store(fh.Filename)
		}
		if fail != nil && fail(call) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"r%d"}`, call)
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &lastName
}

func liveGateway(t *testing.T, backendURL string, cfg LiveConfig) *httptest.Server {
	t.Helper()
	return liveGatewayFor(t, backendURL, false, cfg)
}

// liveGatewayFor: needsWAV=false is a backend that takes Ogg as-is (passthrough).
func liveGatewayFor(t *testing.T, backendURL string, needsWAV bool, cfg LiveConfig) *httptest.Server {
	t.Helper()
	g := makeGateway(backendURL, needsWAV, 50<<20)
	g.live = cfg
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)
	return srv
}

// streamPCM sends pcm in 682-byte frames (like the app), then done; returns all text frames.
func streamPCM(t *testing.T, srv *httptest.Server, query string, pcm []byte, pace time.Duration) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, query), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	var mu sync.Mutex
	var got []string
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				mu.Lock()
				got = append(got, string(data))
				mu.Unlock()
			}
		}
	}()
	for i := 0; i < len(pcm); i += 682 {
		end := i + 682
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, pcm[i:end]); err != nil {
			t.Fatalf("write: %v", err)
		}
		if pace > 0 && (i/682)%25 == 0 {
			time.Sleep(pace)
		}
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck
	<-readDone
	mu.Lock()
	defer mu.Unlock()
	return got
}

func lastResultText(t *testing.T, frames []string) string {
	t.Helper()
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	var r struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal([]byte(frames[len(frames)-1]), &r) //nolint:errcheck
	if r.Type != "" {
		t.Fatalf("last frame is %q, not a result: %v", r.Type, frames)
	}
	return r.Text
}

func TestStreaming_BuiltinPreview_InterimsThenResult(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamPCM(t, srv, "preview=cloud", pcmPattern(4000, 1000, 3000), 20*time.Millisecond)
	if interimCount(t, frames) == 0 {
		t.Fatalf("no interim frames: %v", frames)
	}
	// The final is the whole-clip request, the last one the backend saw.
	if got, want := lastResultText(t, frames), fmt.Sprintf("r%d", atomic.LoadInt32(calls)); got != want {
		t.Fatalf("final %q, want the whole-clip answer %q", got, want)
	}
}

func TestStreaming_BuiltinPreview_IgnoredWithoutParam(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamPCM(t, srv, "", pcmPattern(4000, 1000, 3000), 20*time.Millisecond)
	if len(frames) != 1 || atomic.LoadInt32(calls) != 1 {
		t.Fatalf("frames %v calls %d: preview must stay off without ?preview=cloud", frames, atomic.LoadInt32(calls))
	}
}

func TestJoinSegments_RepairsMidSentenceCuts(t *testing.T) {
	// A backend punctuates each segment as a sentence; this is a dictation with mid-sentence pauses.
	got := joinSegments([]string{
		"The plan for the trip is.", "After the second meeting.", "To book the train at a low fare and.",
		"We leave within two weeks.", "If the forecast shows.", "Heavy rain.", "We will take the car.",
		"I also want a paper map.", "Copy Mr.", "Smith?", "Done.",
	}, true)
	// Only function words are lowercased; "Heavy" keeps its capital (it could be a name).
	want := "The plan for the trip is after the second meeting to book the train at a low fare and " +
		"we leave within two weeks. If the forecast shows Heavy rain. We will take the car. " +
		"I also want a paper map. Copy Mr. Smith? Done."
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestJoinSegments_KeepsCapitalsThatMatter(t *testing.T) {
	if got := joinSegments([]string{"Book an.", "SUV for the weekend.", "And.", "I will call."}, true); got != "Book an SUV for the weekend and I will call." {
		t.Fatalf("got %q", got)
	}
	// Names after a removed stop keep their capital.
	if got := joinSegments([]string{"He was introduced to.", "John at the office.", "Seen with.", "Émile."}, true); got != "He was introduced to John at the office. Seen with Émile." {
		t.Fatalf("got %q", got)
	}
	// Real sentence ends stay: "not", "her", "that" can end a sentence; "When" can start one.
	if got := joinSegments([]string{"The printer did not.", "Her laptop was fine.", "I told her.", "When she called, I said that.", "Then we left."}, true); got != "The printer did not. Her laptop was fine. I told her. When she called, I said that. Then we left." {
		t.Fatalf("got %q", got)
	}
	if got := joinSegments([]string{"", " Hello. ", ""}, true); got != "Hello." {
		t.Fatalf("got %q", got)
	}
}

func TestJoinSegments_OnlyRepairsEnglish(t *testing.T) {
	// Czech "a" (and) / "to" (it): "Je to." is a whole sentence. No repair outside English.
	in := []string{"Je to.", "Über alles a.", "Říkal to."}
	if got := joinSegments(in, false); got != "Je to. Über alles a. Říkal to." {
		t.Fatalf("got %q", got)
	}
}

func feedChunksNoWait(e *liveEngine, pcm []byte) {
	for len(pcm) > 0 {
		n := min(3200, len(pcm))
		e.Write(pcm[:n]) //nolint:errcheck
		pcm = pcm[n:]
	}
}

func TestLiveConfigFromEnv_ClampsNonsense(t *testing.T) {
	t.Setenv("DICTION_PREVIEW_INTERVAL_MS", "0")
	t.Setenv("DICTION_SEGMENT_PAUSE_MS", "-5")
	t.Setenv("DICTION_SEGMENT_MIN_MS", "9000")
	t.Setenv("DICTION_SEGMENT_MAX_MS", "100")
	c := LiveConfigFromEnv()
	if c.PartialEveryMs < 250 || c.PauseMs < 200 || c.MaxSegMs < c.MinSegMs {
		t.Fatalf("not clamped: %+v", c)
	}
}

func TestLiveEngine_SegmentReplyAfterGivingUpDoesNotPanic(t *testing.T) {
	asr := &fakeASR{delay: 500 * time.Millisecond}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, nil)
	defer e.Close()
	feedChunksNoWait(e, pcmPattern(4000, 1000)) // a cut: the segment request starts
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.mu.Lock()
		busy := e.busy
		e.mu.Unlock()
		if busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// While it is in flight, talk past the no-pause cap: the engine gives up and frees
	// its audio. The late reply must then be ignored, not committed into a nil buffer.
	feedChunksNoWait(e, pcmPattern(liveMaxOpenMs+2000))
	e.Close() // joins the worker: the late reply has been handled
	e.mu.Lock()
	defer e.mu.Unlock()
	if !errors.Is(e.failed, errLiveNoPause) || len(e.texts) != 0 {
		t.Fatalf("failed = %v, texts = %q; want the no-pause failure and nothing committed", e.failed, e.texts)
	}
}

func previewConfig() LiveConfig {
	c := testLiveConfig()
	c.Preview = true
	return c
}

// interimCount counts interim frames; none may be empty or follow the result.
func interimCount(t *testing.T, frames []string) int {
	t.Helper()
	n := 0
	for i, f := range frames {
		var r struct{ Type, Text string }
		json.Unmarshal([]byte(f), &r) //nolint:errcheck
		if r.Type != "interim" {
			continue
		}
		n++
		if r.Text == "" {
			t.Fatal("empty interim")
		}
		if i == len(frames)-1 {
			t.Fatal("interim after the result")
		}
	}
	return n
}

func TestLiveEngine_CommitsSegmentsWhileRecording(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 4000, 1000, 2000))
	e.mu.Lock()
	text := e.committedText()
	open := float64(len(e.pcm)) / 2 / liveSampleRate
	e.mu.Unlock()
	if text != "part1. part2." {
		t.Fatalf("committed %q", text)
	}
	// Committed audio is dropped: only the part after the second pause is still held.
	if d := asr.durations(); len(d) != 2 || open > 3.0 || math.Abs(d[0]+d[1]+open-12.0) > 0.05 {
		t.Fatalf("requests %v, %.2fs still open, of 12 s", d, open)
	}
}

func TestLiveEngine_SegmentFailureStopsTheEngine(t *testing.T) {
	asr := &fakeASR{fail: func(call int) error {
		if call == 0 {
			return errors.New("backend returned 500")
		}
		return nil
	}}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failed == nil || len(e.pcm) != 0 || len(e.texts) != 0 {
		t.Fatalf("failed = %v, %d bytes held, texts %q; want a stopped, empty engine", e.failed, len(e.pcm), e.texts)
	}
}

func TestLiveEngine_GivesUpWithoutPauses(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(liveMaxOpenMs+2000))
	e.mu.Lock()
	defer e.mu.Unlock()
	if !errors.Is(e.failed, errLiveNoPause) || len(e.pcm) != 0 {
		t.Fatalf("failed = %v with %d bytes held after 92 s without a pause", e.failed, len(e.pcm))
	}
}

// A segment that comes back with more text than its audio can hold is not committed.
func TestLiveEngine_InventedSegmentStopsTheEngine(t *testing.T) {
	invent := func(ctx context.Context, wav []byte) (string, error) {
		return strings.Repeat("Some Term, ", 60), nil
	}
	e := newLiveEngine(context.Background(), testLiveConfig(), invent, nil)
	defer e.Close()
	feedLive(t, e, pcmPattern(4000, 1000, 2000))
	e.mu.Lock()
	defer e.mu.Unlock()
	if !errors.Is(e.failed, errSTTHallucination) || len(e.texts) != 0 {
		t.Fatalf("failed = %v, texts %q", e.failed, e.texts)
	}
}

func TestImplausibleText(t *testing.T) {
	if !implausibleText(strings.Repeat("x", 60), make([]byte, 44+2*liveSampleRate)) {
		t.Fatal("60 chars in 1 s should be implausible")
	}
	if implausibleText("Please send the report to the front office and copy the whole team.", make([]byte, 44+5*2*liveSampleRate)) {
		t.Fatal("a normal 5 s sentence flagged")
	}
}

// A writer that falls behind gets the newest interim, and the engine never blocks on it.
func TestLiveEngine_UnreadInterimIsReplaced(t *testing.T) {
	asr := &fakeASR{}
	e := newLiveEngine(context.Background(), testLiveConfig(), asr.transcribe, asr.transcribe)
	defer e.Close()
	pcm := pcmPattern(2000)
	for i := 0; i < len(pcm); i += 3200 { // 100 ms per write, paced so each can start a partial
		e.Write(pcm[i:min(i+3200, len(pcm))]) //nolint:errcheck
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(asr.durations()); n < 3 {
		t.Fatalf("%d partial requests with nobody reading; want the engine to keep going", n)
	}
	select {
	case s := <-e.interims:
		if s == "" || s == "part1." {
			t.Fatalf("interim %q, want a later one than the first", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no interim")
	}
}

func TestPreviewCapabilities(t *testing.T) {
	prev := PreviewRelay
	PreviewRelay = nil
	t.Cleanup(func() { PreviewRelay = prev })
	g := makeGateway("http://127.0.0.1:1", false, 1<<20)
	if pcm, opus := g.PreviewCapabilities(); pcm || opus {
		t.Fatalf("off by default, got pcm=%v opus=%v", pcm, opus)
	}
	g.live.Preview = true
	if pcm, opus := g.PreviewCapabilities(); !pcm || opus != (lookupFfmpeg() != "") {
		t.Fatalf("built-in preview: pcm=%v opus=%v (ffmpeg %q)", pcm, opus, lookupFfmpeg())
	}
	// A relay hook serves PCM sessions only, whatever DICTION_PREVIEW says.
	PreviewRelay = func(context.Context, string) PreviewSession { return nil }
	if pcm, opus := g.PreviewCapabilities(); !pcm || opus {
		t.Fatalf("relay: pcm=%v opus=%v", pcm, opus)
	}
}

// DICTION_PREVIEW_URL: partials go to the separate backend, the final to the session's.
func TestStreaming_BuiltinPreview_SeparateBackend(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	fast, fastCalls, _ := segmentBackend(t, nil)
	cfg := previewConfig()
	cfg.PreviewURL, cfg.PreviewModel = fast.URL, "fast"
	srv := liveGateway(t, backend.URL, cfg)
	frames := streamPCM(t, srv, "preview=cloud", pcmPattern(2000), 20*time.Millisecond) // no pause: partials only
	lastResultText(t, frames)
	if atomic.LoadInt32(fastCalls) == 0 || atomic.LoadInt32(calls) != 1 {
		t.Fatalf("preview backend calls %d, session backend calls %d; want partials on the first and one final on the second",
			atomic.LoadInt32(fastCalls), atomic.LoadInt32(calls))
	}
}

func TestStreaming_BuiltinPreview_BadPreviewURLUsesSessionBackend(t *testing.T) {
	backend, calls, _ := segmentBackend(t, nil)
	cfg := previewConfig()
	cfg.PreviewURL = "not a url"
	srv := liveGateway(t, backend.URL, cfg)
	frames := streamPCM(t, srv, "preview=cloud", pcmPattern(2000), 20*time.Millisecond)
	lastResultText(t, frames)
	if n := atomic.LoadInt32(calls); n < 2 {
		t.Fatalf("session backend calls = %d, want partials and the final", n)
	}
}

// testOgg renders seconds of "3 s tone, 1 s silence" as Ogg Opus in 200 ms pages.
func testOgg(t *testing.T, seconds int) []byte {
	t.Helper()
	cmd := exec.Command(ensureFfmpeg(t), "-loglevel", "error", "-f", "lavfi",
		"-i", fmt.Sprintf("aevalsrc=0.1*sin(2*PI*220*t)*lt(mod(t\\,4)\\,3):s=16000:d=%d", seconds),
		"-c:a", "libopus", "-b:a", "16k", "-page_duration", "200000", "-f", "ogg", "pipe:1")
	var ogg bytes.Buffer
	cmd.Stdout = &ogg
	if err := cmd.Run(); err != nil {
		t.Skipf("libopus encode failed: %v", err)
	}
	return ogg.Bytes()
}

// streamOpusFrames sends ogg in paced chunks on an Opus session, then done, and returns
// all text frames. With waitInterim it sends done only once an interim has arrived.
func streamOpusFrames(t *testing.T, srv *httptest.Server, query string, ogg []byte, waitInterim bool) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, query), &websocket.DialOptions{Subprotocols: []string{opusSubprotocol}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	var mu sync.Mutex
	var got []string
	readDone, interim := make(chan struct{}), make(chan struct{}, 1)
	go func() {
		defer close(readDone)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			mu.Lock()
			got = append(got, string(data))
			mu.Unlock()
			if strings.Contains(string(data), "\"interim\"") {
				select {
				case interim <- struct{}{}:
				default:
				}
			}
		}
	}()
	for i := 0; i < len(ogg); i += 2048 {
		if err := conn.Write(ctx, websocket.MessageBinary, ogg[i:min(i+2048, len(ogg))]); err != nil {
			t.Fatalf("write: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waitInterim {
		select {
		case <-interim:
		case <-time.After(10 * time.Second):
			t.Fatal("no interim frame on an Opus session")
		}
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck
	<-readDone
	mu.Lock()
	defer mu.Unlock()
	return got
}

// Opus to a backend that takes Ogg as-is: interims come from a side decode, and the
// final request still carries the original Ogg, exactly as without live text.
func TestStreaming_BuiltinPreview_OpusKeepsOriginalForPassthrough(t *testing.T) {
	ogg := testOgg(t, 10)
	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamOpusFrames(t, srv, "codec=opus&preview=cloud", ogg, true)
	if interimCount(t, frames) == 0 {
		t.Fatalf("no interim frames: %v", frames)
	}
	if got, want := lastResultText(t, frames), fmt.Sprintf("r%d", atomic.LoadInt32(calls)); got != want {
		t.Fatalf("final %q, want the whole-clip answer %q", got, want)
	}
	if name, _ := lastName.Load().(string); name != "audio.ogg" {
		t.Fatalf("final request sent %q, want the original audio.ogg", name)
	}
}

// Opus to a backend that needs WAV: the one decode feeds both the final and the interims.
func TestStreaming_BuiltinPreview_OpusDecodedForWAVBackend(t *testing.T) {
	ogg := testOgg(t, 10)
	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGatewayFor(t, backend.URL, true, previewConfig())
	frames := streamOpusFrames(t, srv, "codec=opus&preview=cloud", ogg, true)
	if interimCount(t, frames) == 0 {
		t.Fatalf("no interim frames: %v", frames)
	}
	if got, want := lastResultText(t, frames), fmt.Sprintf("r%d", atomic.LoadInt32(calls)); got != want {
		t.Fatalf("final %q, want the whole-clip answer %q", got, want)
	}
	if name, _ := lastName.Load().(string); name != "audio.wav" {
		t.Fatalf("final request sent %q, want the decoded audio.wav", name)
	}
}

// Without ?preview=cloud an Opus session is untouched: one request, the original Ogg.
func TestStreaming_BuiltinPreview_OpusIgnoredWithoutParam(t *testing.T) {
	ogg := testOgg(t, 6)
	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamOpusFrames(t, srv, "codec=opus", ogg, false)
	if name, _ := lastName.Load().(string); len(frames) != 1 || atomic.LoadInt32(calls) != 1 || name != "audio.ogg" {
		t.Fatalf("frames %v, calls %d, sent %q", frames, atomic.LoadInt32(calls), name)
	}
}

// The side decode is best effort: with every slot taken the session runs without live
// text, and the final is unaffected.
func TestStreaming_BuiltinPreview_OpusDecodeSlotsBusy(t *testing.T) {
	ogg := testOgg(t, 6)
	for {
		release, ok := tryLiveDecodeSlot()
		if !ok {
			break
		}
		t.Cleanup(release)
	}
	backend, calls, lastName := segmentBackend(t, nil)
	srv := liveGateway(t, backend.URL, previewConfig())
	frames := streamOpusFrames(t, srv, "codec=opus&preview=cloud", ogg, false)
	if name, _ := lastName.Load().(string); len(frames) != 1 || atomic.LoadInt32(calls) != 1 || name != "audio.ogg" {
		t.Fatalf("frames %v, calls %d, sent %q", frames, atomic.LoadInt32(calls), name)
	}
}
