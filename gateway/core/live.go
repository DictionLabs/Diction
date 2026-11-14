package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Live transcription on the batch socket, for self-hosted backends.
//
// One engine per /v1/audio/stream session watches the PCM as it arrives (16 kHz s16le
// mono: raw PCM frames, or Opus decoded by the existing ffmpeg pipe) and cuts it into
// segments at pauses. A single background worker sends one request at a time to the
// session's own backend, so it never competes with itself for the GPU. It serves two
// features, both off by default:
//
//   - Segmented final (DICTION_SEGMENTED_FINAL): segments are transcribed while the user
//     is still talking, so after `done` only the tail since the last pause is left.
//     Any failure falls back to today's single whole-clip request.
//   - Built-in preview (DICTION_PREVIEW=chunked): the open segment is re-transcribed
//     about once a second and `{"type":"interim"}` frames carry the whole transcript so
//     far (committed segments + the open one). It answers `?preview=cloud` when no
//     PreviewRelay hook is installed, and works with any OpenAI-compatible backend.

// LiveConfig holds the env-tunable settings. The zero value disables everything.
type LiveConfig struct {
	Segmented bool // DICTION_SEGMENTED_FINAL
	Preview   bool // DICTION_PREVIEW=chunked

	PauseMs      int // a pause this long ends a segment (DICTION_SEGMENT_PAUSE_MS)
	MinSegMs     int // segments shorter than this are never cut (DICTION_SEGMENT_MIN_MS)
	MaxSegMs     int // past this length a short pause is enough (DICTION_SEGMENT_MAX_MS)
	ShortPauseMs int // the pause needed past MaxSegMs
	// The first piece is at least this long (DICTION_SEGMENT_FIRST_MIN_MS; 0 = MinSegMs), so a
	// dictation shorter than it is transcribed whole and only longer ones go to pieces.
	FirstMinSegMs int

	PartialEveryMs int // preview: re-transcribe the open segment after this much new audio
	// Preview partials in flight across the whole gateway (DICTION_PREVIEW_MAX_CONCURRENT).
	// A session whose partial finds them all taken skips it; segments and finals never wait.
	PartialMaxConcurrent int
	partialSlots         chan struct{} // nil = no limit (tests); shared by every copy of the config

	// Optional separate backend for preview partials (e.g. the ASR server directly,
	// skipping slower post-processing). Empty URL = the session's own backend.
	PreviewURL   string
	PreviewModel string
	PreviewAuth  string
}

// LiveConfigFromEnv reads the DICTION_SEGMENT* and DICTION_PREVIEW* variables. Values
// below a sane minimum are raised to it (a 0 ms preview interval would hammer the backend).
func LiveConfigFromEnv() LiveConfig {
	c := LiveConfig{
		Segmented:            EnvBoolOrDefault("DICTION_SEGMENTED_FINAL", false),
		Preview:              strings.EqualFold(EnvOrDefault("DICTION_PREVIEW", "off"), "chunked"),
		PauseMs:              EnvIntOrDefault("DICTION_SEGMENT_PAUSE_MS", 700),
		MinSegMs:             EnvIntOrDefault("DICTION_SEGMENT_MIN_MS", 8000),
		MaxSegMs:             EnvIntOrDefault("DICTION_SEGMENT_MAX_MS", 20000),
		ShortPauseMs:         EnvIntOrDefault("DICTION_SEGMENT_SHORT_PAUSE_MS", 300),
		FirstMinSegMs:        EnvIntOrDefault("DICTION_SEGMENT_FIRST_MIN_MS", 0),
		PartialEveryMs:       EnvIntOrDefault("DICTION_PREVIEW_INTERVAL_MS", 1000),
		PartialMaxConcurrent: EnvIntOrDefault("DICTION_PREVIEW_MAX_CONCURRENT", 2),
		PreviewURL:           EnvOrDefault("DICTION_PREVIEW_URL", ""),
		PreviewModel:         EnvOrDefault("DICTION_PREVIEW_MODEL", ""),
		PreviewAuth:          EnvOrDefault("DICTION_PREVIEW_AUTH", ""),
	}
	atLeast := func(v *int, min int) {
		if *v < min {
			*v = min
		}
	}
	atLeast(&c.PauseMs, 200)
	atLeast(&c.ShortPauseMs, 100)
	atLeast(&c.MinSegMs, 1000)
	atLeast(&c.MaxSegMs, c.MinSegMs)
	atLeast(&c.PartialEveryMs, 250)
	atLeast(&c.PartialMaxConcurrent, 1)
	c.partialSlots = make(chan struct{}, c.PartialMaxConcurrent)
	return c
}

const (
	liveSampleRate   = 16000
	liveFrameSamples = 320 // 20 ms
	liveFrameMs      = 20
	liveFrameBytes   = liveFrameSamples * 2
	// The noise floor is the quietest frame of the last 3 s; speech is >10 dB above it.
	liveFloorWindow   = 150
	liveSpeechAboveDB = 10.0
	liveThresholdMin  = -60.0 // below this is silence whatever the floor
	liveThresholdMax  = -30.0 // above this is speech whatever the floor
	liveMinVoicedMs   = 300   // a segment needs this much speech to be cut
	// The tail is skipped only if it is (near) silence: fewer than 60 ms of frames even
	// 6 dB above the floor, with no upper clamp, so a quiet last word is still sent.
	liveActiveAboveDB = 6.0
	liveTailActiveMs  = 60
	// Audio held without any cut. Past this the engine gives up (whole clip instead), so
	// a long stretch without pauses cannot grow memory without bound.
	liveMaxOpenMs = 90000
)

// pauseDetector finds cut points in 16 kHz s16le PCM. Positions are in samples.
type pauseDetector struct {
	cfg LiveConfig

	rem      []byte
	pos      int64 // samples processed
	floor    [liveFloorWindow]float64
	floorN   int
	segStart int64
	voiced   int // speech frames since segStart
	active   int // frames since segStart at least liveActiveAboveDB over the floor
	silence  int // consecutive silent frames
	silStart int64
}

type liveCut struct {
	at     int64 // sample position
	active int   // frames above the weaker "anything there?" threshold
}

func frameDBFS(frame []byte) float64 {
	var sum float64
	n := len(frame) / 2
	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(frame[2*i:])))
		sum += s * s
	}
	rms := math.Sqrt(sum / float64(n))
	if rms < 1 {
		return -96
	}
	return 20 * math.Log10(rms/32768)
}

// thresholds returns the speech threshold (cuts) and the weaker activity threshold (tail).
func (d *pauseDetector) thresholds() (speech, active float64) {
	n := min(d.floorN, liveFloorWindow)
	lo := d.floor[0]
	for i := 1; i < n; i++ {
		lo = math.Min(lo, d.floor[i])
	}
	speech = math.Max(liveThresholdMin, math.Min(liveThresholdMax, lo+liveSpeechAboveDB))
	active = math.Max(liveThresholdMin, lo+liveActiveAboveDB)
	return speech, active
}

// push consumes PCM and returns any cuts it completes.
func (d *pauseDetector) push(p []byte) []liveCut {
	var cuts []liveCut
	if len(d.rem) > 0 {
		p = append(d.rem, p...)
		d.rem = nil
	}
	for len(p) >= liveFrameBytes {
		frame := p[:liveFrameBytes]
		p = p[liveFrameBytes:]
		db := frameDBFS(frame)
		d.floor[d.floorN%liveFloorWindow] = db
		d.floorN++
		frameStart := d.pos
		d.pos += liveFrameSamples
		speech, active := d.thresholds()
		if db > active {
			d.active++
		}
		if db > speech {
			d.voiced++
			d.silence = 0
			continue
		}
		if d.silence == 0 {
			d.silStart = frameStart
		}
		d.silence++
		segMs := int((d.pos - d.segStart) * 1000 / liveSampleRate)
		minMs, maxMs := d.cfg.MinSegMs, d.cfg.MaxSegMs
		if d.segStart == 0 && d.cfg.FirstMinSegMs > minMs {
			// First piece: longer minimum, and the maximum moves up by the same amount.
			if maxMs > 0 {
				maxMs += d.cfg.FirstMinSegMs - minMs
			}
			minMs = d.cfg.FirstMinSegMs
		}
		need := d.cfg.PauseMs
		if maxMs > 0 && segMs >= maxMs {
			need = d.cfg.ShortPauseMs
		}
		if d.voiced*liveFrameMs >= liveMinVoicedMs && segMs >= minMs && d.silence*liveFrameMs >= need {
			// Cut in the middle of the pause seen so far: both sides keep some silence.
			at := d.silStart + int64(d.silence*liveFrameSamples/2)
			cuts = append(cuts, liveCut{at: at, active: d.active})
			d.segStart = at
			d.voiced, d.active = 0, 0
		}
	}
	if len(p) > 0 {
		d.rem = append([]byte(nil), p...)
	}
	return cuts
}

// liveMaxCharsPerSec bounds how much text a stretch of audio can hold. Fast dictation
// is under 20 characters a second; more than this is the backend inventing text. Seen
// with Qwen3-ASR: on a short piece it can append its whole vocabulary-hint prompt.
const liveMaxCharsPerSec = 25

// implausibleText reports a result that cannot be what was said in wav: degenerate
// repetition, or more text than the audio could hold. The caller then treats the piece
// as failed (so the whole clip is transcribed instead).
func implausibleText(text string, wav []byte) bool {
	secs := float64(len(wav)-44) / 2 / liveSampleRate
	return hasDegenerateRepetition(text) || utf8.RuneCountInString(text) > int(liveMaxCharsPerSec*secs)+20
}

// liveTranscribeFunc transcribes one WAV (16 kHz mono s16). It must honour ctx.
type liveTranscribeFunc func(ctx context.Context, wav []byte) (string, error)

// liveEngine runs one session's segmentation and its single background worker.
type liveEngine struct {
	cfg        LiveConfig
	final      liveTranscribeFunc // segments and tail (the session's backend, full prompt)
	partial    liveTranscribeFunc // preview partials; nil = no preview
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	workerDone chan struct{}
	closeOnce  sync.Once

	mu        sync.Mutex
	det       pauseDetector
	pcm       []byte // audio from base on (committed audio is dropped)
	base      int64  // sample position of pcm[0]
	pending   []liveCut
	texts     []string // committed segment texts, in order
	committed int64    // sample position up to which texts cover the audio
	failed    error
	finishing bool
	busy      bool   // a segment job is running (tests wait on it)
	lang      string // session language: joinSegments' English repair and spaceless scripts

	partialCancel  context.CancelFunc
	partialFrom    int64 // committed position the last partial was computed from
	partialAt      int64 // audio length when the last partial was started
	interims       chan string
	interimsClosed bool

	segments int // committed so far
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

// Write feeds PCM. Safe to call from any one goroutine at a time; never blocks on the
// backend. After a failure or Finish it discards the audio (the handler keeps its own copy).
func (e *liveEngine) Write(p []byte) (int, error) {
	e.mu.Lock()
	if !e.finishing && e.failed == nil {
		e.pcm = append(e.pcm, p...)
		e.pending = append(e.pending, e.det.push(p)...)
		if len(e.pending) == 0 && (e.audioEnd()-e.committed)*1000/liveSampleRate > liveMaxOpenMs {
			e.fail(errLiveNoPause)
		}
	}
	e.mu.Unlock()
	e.poke()
	return len(p), nil
}

var errLiveNoPause = errors.New("live: no pause to cut at")

// fail marks the engine failed and frees its audio. Caller holds mu.
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

// audioEnd is the sample position of the end of the received audio. Caller holds mu.
func (e *liveEngine) audioEnd() int64 { return e.base + int64(len(e.pcm)/2) }

// wav returns a WAV of [from, to). Caller holds mu.
func (e *liveEngine) wav(from, to int64) []byte {
	a := (from - e.base) * 2
	b := (to - e.base) * 2
	var buf bytes.Buffer
	buf.Grow(int(b-a) + 44)
	WriteWAVHeader(&buf, int(b-a)) //nolint:errcheck — bytes.Buffer cannot fail
	buf.Write(e.pcm[a:b])
	return buf.Bytes()
}

// commit records a segment's text and drops its audio. Caller holds mu.
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

// Words that (almost) never end a sentence, and words that (almost) never start one.
// A backend punctuates every segment as if it were complete, so a cut at a mid-sentence
// pause comes back as "The plan is. After the MRI. To start…". These lists decide when
// that full stop is an artefact of the cut. They are deliberately short: a word that can
// end a sentence ("not", "her", "that", "will") or start one ("When", "After", "Then")
// is left out, because joining two real sentences is worse than a stray full stop.
var (
	// Not here, because they often end a spoken sentence: is/are/was/were/am/been
	// ("I know what it is."), to ("I'd love to.").
	segDanglingEnd = setOf("a", "an", "the", "and", "or", "nor", "of", "for", "with", "from",
		"into", "if", "because", "than", "my", "our", "your", "their", "shows", "showed",
		"including", "per", "plus", "versus", "vs")
	// Not here, because they often start one: of ("Of course…"), to ("To be fair…"),
	// plus ("Plus, it's cheaper.").
	segContinuingStart = setOf("and", "or", "nor", "than", "whom", "whose", "versus", "including")
	// After a removed full stop, only these words are lowercased. Anything else keeps its
	// capital, because it may be a name ("lunch with. John" → "lunch with John").
	segLowerable = setOf("a", "an", "the", "and", "or", "nor", "but", "so", "to", "of", "for",
		"with", "without", "from", "in", "on", "at", "by", "into", "after", "before", "when",
		"while", "then", "if", "because", "than", "as", "that", "this", "these", "those", "it",
		"its", "we", "he", "she", "they", "there", "his", "her", "our", "their", "my", "your",
		"which", "who", "whom", "whose", "what", "plus", "versus", "including", "also")
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[0], ",;:.!?")
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return strings.TrimRight(f[len(f)-1], ".,;:")
}

// spacelessLang reports a language written without spaces between words.
func spacelessLang(lang string) bool {
	l := strings.ToLower(lang)
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	switch l {
	case "zh", "ja", "yue", "th":
		return true
	}
	return false
}

// spacelessRune reports a character of a script written without spaces (Chinese,
// Japanese, Thai), including CJK and fullwidth punctuation such as 。and ，.
func spacelessRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// segmentSeparator is what goes between two segments: nothing where the text is
// Chinese, Japanese or Thai, a space otherwise. In a zh/ja/yue/th session one such
// character at the seam is enough; in any other session (auto-detect included) both
// sides must be one, so mixed text keeps its spaces.
func segmentSeparator(prev, next, lang string) string {
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(next)
	a, b := spacelessRune(last), spacelessRune(first)
	if (a && b) || (spacelessLang(lang) && (a || b)) {
		return ""
	}
	return " "
}

// joinSegments joins segment texts with spaces (none in Chinese, Japanese or Thai text,
// see segmentSeparator). For English (the word lists are English) it also removes the
// full stop a cut put in mid-sentence: when a segment ends in a word that cannot end a
// sentence, or the next starts with one that cannot start one. The next word is then
// lowercased only if it is a common function word, so names and acronyms keep their
// capitals. Question and exclamation marks are always kept.
func joinSegments(texts []string, lang string) string {
	english := strings.HasPrefix(strings.ToLower(lang), "en")
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(t)
			continue
		}
		prev := b.String()
		next := strings.ToLower(firstWord(t))
		if english && strings.HasSuffix(prev, ".") && !strings.HasSuffix(prev, "..") &&
			(segDanglingEnd[strings.ToLower(lastWord(prev))] || segContinuingStart[next]) {
			b.Reset()
			b.WriteString(strings.TrimSuffix(prev, "."))
			if segLowerable[next] {
				r, size := utf8.DecodeRuneInString(t)
				t = string(unicode.ToLower(r)) + t[size:]
			}
		}
		b.WriteString(segmentSeparator(b.String(), t, lang))
		b.WriteString(t)
	}
	return b.String()
}

// emit publishes an interim, replacing any the writer has not taken yet. Caller holds mu.
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

// step runs at most one job. Segments come first; a partial runs only when no
// segment is waiting. Returns whether it did anything.
func (e *liveEngine) step() bool {
	e.mu.Lock()
	if e.finishing || e.failed != nil || e.ctx.Err() != nil {
		e.mu.Unlock()
		return false
	}
	if len(e.pending) > 0 {
		// Everything cut so far goes in one request (normally just one segment).
		last := e.pending[len(e.pending)-1]
		e.pending = nil
		from := e.committed
		wav := e.wav(from, last.at)
		e.busy = true
		e.mu.Unlock()

		text, err := e.final(e.ctx, wav)
		if err == nil && implausibleText(text, wav) {
			err = errSTTHallucination
		}

		e.mu.Lock()
		e.busy = false
		if e.failed != nil { // gave up meanwhile (Write's no-pause cap): the audio is gone
			e.mu.Unlock()
			return false
		}
		if err != nil {
			e.fail(err)
			e.mu.Unlock()
			return false
		}
		e.commit(last.at, text)
		e.segments++
		e.emit(e.committedText())
		e.mu.Unlock()
		return true
	}
	if e.partial == nil || e.interimsClosed {
		e.mu.Unlock()
		return false
	}
	end := e.audioEnd()
	open := end - e.committed
	grown := end - e.partialAt
	if e.partialFrom != e.committed {
		grown = open
	}
	if grown*1000/liveSampleRate < int64(e.cfg.PartialEveryMs) || e.det.voiced*liveFrameMs < liveMinVoicedMs {
		e.mu.Unlock()
		return false
	}
	// Each partial re-sends the whole open segment, so the gateway-wide cap is what
	// keeps many sessions from tying up a self-hoster's GPU. Busy: skip this one; the
	// next audio write tries again.
	if slots := e.cfg.partialSlots; slots != nil {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			e.mu.Unlock()
			return false
		}
	}
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
	// A segment may have committed meanwhile; this partial is then stale.
	if err == nil && e.committed == from && !implausibleText(text, wav) {
		joined := joinSegments(append(append([]string(nil), e.texts...), text), e.lang)
		e.emit(joined)
	}
	e.mu.Unlock()
	return true
}

// stopInterims closes the interim channel; the engine keeps running for the final.
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

// liveFinish describes how the segmented final went, for logs.
type liveFinish struct {
	segments int
	tailSec  float64
	// invented: the tail was the whole recording and came back with more text than the
	// audio can hold. The whole-clip request would send the same audio to the same
	// backend, so the caller answers with no text instead.
	invented bool
}

// Finish ends recording: it waits for an in-flight segment, transcribes the tail and
// returns the joined text. ok=false means the caller must use the whole-clip request.
//
// The whole of Finish has a budget (5 s plus half the audio still open), so a stalled
// backend costs at most that before the whole-clip request is tried.
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
	// Pending cuts that never started are simply part of the tail.
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
			st.invented = err == nil && st.segments == 0 && !hasDegenerateRepetition(text)
			return "", false, st
		}
		if t := strings.TrimSpace(text); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		// Nothing heard, or the detector misjudged the audio: let the whole clip decide.
		return "", false, st
	}
	return joinSegments(texts, e.lang), true, st
}

// Close stops the worker and any request in flight. Idempotent.
func (e *liveEngine) Close() {
	e.closeOnce.Do(func() {
		e.cancel()
		<-e.workerDone
		e.stopInterims()
	})
}

// livePreview adapts the engine to PreviewSession. The handler feeds the engine
// directly (it also sees decoded Opus), so Send is a no-op. Stop ends the interims
// only; the handler owns the engine's lifetime (the segmented final may still need it).
type livePreview struct{ e *liveEngine }

func (p livePreview) Send([]byte)             {}
func (p livePreview) Interims() <-chan string { return p.e.interims }
func (p livePreview) Stop()                   { p.e.stopInterims() }

// PreviewCapabilities reports what /v1/models should advertise: whether preview=cloud
// gets interim frames on PCM sessions, and on Opus sessions. It mirrors the stream
// handler: a PreviewRelay serves PCM only; the built-in preview serves PCM, and Opus
// when ffmpeg is there to decode it as it arrives.
func (g *Gateway) PreviewCapabilities() (pcm, opus bool) {
	if PreviewRelay != nil {
		return true, false
	}
	if !g.live.Preview {
		return false, false
	}
	return true, lookupFfmpeg() != ""
}

// startLive opens a session's engine against the backend routing chose, with the same
// language, prompt and custom words the whole-clip request would send — so segment
// text goes through exactly the same backend-side processing as today's final.
func (g *Gateway) startLive(ctx context.Context, target *url.URL, backend *Backend, language, upstreamLanguage, contextJSON string, preview bool) *liveEngine {
	prompt := buildWhisperPrompt(contextJSON)
	customWords := buildCustomWordsField(contextJSON)
	final := func(ctx context.Context, wav []byte) (string, error) {
		return g.proxyToBackend(ctx, target, audioPayload{data: wav, filename: "audio.wav"}, backend, upstreamLanguage, prompt, customWords)
	}
	var partial liveTranscribeFunc
	if preview {
		partial = final
		if g.live.PreviewURL != "" {
			if pu, err := url.Parse(g.live.PreviewURL); err == nil && pu.Host != "" {
				pb := &Backend{Name: "preview", ForwardModel: g.live.PreviewModel, AuthHeader: g.live.PreviewAuth}
				partial = func(ctx context.Context, wav []byte) (string, error) {
					return g.proxyToBackend(ctx, pu, audioPayload{data: wav, filename: "audio.wav"}, pb, upstreamLanguage, "", "")
				}
			} else {
				log.Printf("live: DICTION_PREVIEW_URL is not a URL; partials use the session backend")
			}
		}
	}
	e := newLiveEngine(ctx, g.live, final, partial)
	e.lang = language
	return e
}

// close is the nil-safe Close for the handler's defer.
func (e *liveEngine) close() {
	if e != nil {
		e.Close()
	}
}

// liveDecodeSlots bounds the best-effort live decoders. It is separate from the Opus
// semaphore so live sessions never delay or reject a decode the backend needs.
var (
	liveDecodeOnce  sync.Once
	liveDecodeSlots chan struct{}
)

// tryLiveDecodeSlot takes a slot without waiting; release must be called if it returns true.
func tryLiveDecodeSlot() (release func(), ok bool) {
	liveDecodeOnce.Do(func() { liveDecodeSlots = make(chan struct{}, cap(getOpusSem())) })
	select {
	case liveDecodeSlots <- struct{}{}:
		return func() { <-liveDecodeSlots }, true
	default:
		return nil, false
	}
}

// startLiveDecoder runs ffmpeg for a session whose original Opus is kept for the backend
// (keepOriginal): its PCM goes only to the live engine, *decoded counts the bytes, and the
// output is always drained so ffmpeg can never block the read loop.
func startLiveDecoder(ctx context.Context, inputFmt string, stderr io.Writer, live *liveEngine, decoded *int64) (*exec.Cmd, io.WriteCloser, chan struct{}, error) {
	cmd := exec.CommandContext(ctx, lookupFfmpeg(), "-loglevel", "error",
		"-f", inputFmt, "-i", "pipe:0", "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, nil, nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(countingWriter{live, decoded}, stdout) //nolint:errcheck — live.Write never fails
	}()
	return cmd, stdin, done, nil
}

type countingWriter struct {
	w io.Writer
	n *int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	atomic.AddInt64(c.n, int64(len(p)))
	return c.w.Write(p)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
