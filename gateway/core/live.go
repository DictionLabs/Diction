package core

import (
	"encoding/binary"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/DictionLabs/Diction/gateway/core/transcript"
)

type LiveConfig struct {
	Segmented bool
	Preview   bool

	PauseMs       int
	MinSegMs      int
	MaxSegMs      int
	ShortPauseMs  int
	FirstMinSegMs int

	PartialEveryMs       int
	PartialMaxConcurrent int
	partialSlots         chan struct{}

	PreviewURL   string
	PreviewModel string
	PreviewAuth  string
}

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
	atLeast := func(v *int, lo int) {
		if *v < lo {
			*v = lo
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
	liveSampleRate    = 16000
	liveFrameSamples  = 320
	liveFrameMs       = 20
	liveFrameBytes    = liveFrameSamples * 2
	liveFloorWindow   = 150
	liveSpeechAboveDB = 10.0
	liveThresholdMin  = -60.0
	liveThresholdMax  = -30.0
	liveMinVoicedMs   = 300
	liveActiveAboveDB = 6.0
	liveTailActiveMs  = 60
	liveMaxOpenMs     = 90000
)

type pauseDetector struct {
	cfg LiveConfig

	rem      []byte
	pos      int64
	floor    [liveFloorWindow]float64
	floorN   int
	segStart int64
	voiced   int
	active   int
	silence  int
	silStart int64
}

type liveCut struct {
	at     int64
	active int
}

func frameDBFS(frame []byte) float64 {
	var sum float64
	n := len(frame) / 2
	for i := 0; i < n; i++ {
		u := binary.LittleEndian.Uint16(frame[2*i:])
		s := float64(u)
		if u >= 1<<15 {
			s -= 1 << 16
		}
		sum += s * s
	}
	rms := math.Sqrt(sum / float64(n))
	if rms < 1 {
		return -96
	}
	return 20 * math.Log10(rms/32768)
}

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

func (d *pauseDetector) push(p []byte) []liveCut {
	var cuts []liveCut
	if len(d.rem) > 0 {
		p = append(d.rem, p...)
		d.rem = nil
	}
	for len(p) >= liveFrameBytes {
		frame := p[:liveFrameBytes]
		p = p[liveFrameBytes:]
		if cut, ok := d.pushFrame(frame); ok {
			cuts = append(cuts, cut)
		}
	}
	if len(p) > 0 {
		d.rem = append([]byte(nil), p...)
	}
	return cuts
}

func (d *pauseDetector) pushFrame(frame []byte) (liveCut, bool) {
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
		return liveCut{}, false
	}
	if d.silence == 0 {
		d.silStart = frameStart
	}
	d.silence++
	segMs := int((d.pos - d.segStart) * 1000 / liveSampleRate)
	minMs, maxMs := d.segmentLimits()
	need := d.cfg.PauseMs
	if maxMs > 0 && segMs >= maxMs {
		need = d.cfg.ShortPauseMs
	}
	if d.voiced*liveFrameMs < liveMinVoicedMs || segMs < minMs || d.silence*liveFrameMs < need {
		return liveCut{}, false
	}
	at := d.silStart + int64(d.silence*liveFrameSamples/2)
	cut := liveCut{at: at, active: d.active}
	d.segStart = at
	d.voiced, d.active = 0, 0
	return cut, true
}

func (d *pauseDetector) segmentLimits() (minMs, maxMs int) {
	minMs, maxMs = d.cfg.MinSegMs, d.cfg.MaxSegMs
	if d.segStart == 0 && d.cfg.FirstMinSegMs > minMs {
		if maxMs > 0 {
			maxMs += d.cfg.FirstMinSegMs - minMs
		}
		minMs = d.cfg.FirstMinSegMs
	}
	return minMs, maxMs
}

const liveMaxCharsPerSec = 25

func implausibleText(text string, wav []byte) bool {
	secs := float64(len(wav)-44) / 2 / liveSampleRate
	return transcript.HasDegenerateRepetition(text) || utf8.RuneCountInString(text) > int(liveMaxCharsPerSec*secs)+20
}
