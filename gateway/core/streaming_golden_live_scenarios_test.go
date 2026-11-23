package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func sgLiveCfg(g *Gateway) {
	g.live = LiveConfig{PauseMs: 700, MinSegMs: 3000, MaxSegMs: 20000, ShortPauseMs: 300, PartialEveryMs: 100, Segmented: true}
}

func sgPCMChunks(pcm []byte, doneLang string) func(c sgClient) {
	return func(c sgClient) {
		rest := pcm
		for len(rest) > 0 {
			n := min(3200, len(rest))
			c.bin(rest[:n])
			rest = rest[n:]
		}
		if doneLang != "" {
			c.doneLang(doneLang)
			return
		}
		c.done()
	}
}

var sgBackendHit = regexp.MustCompile(`hit \d+:`)

// sgWholeClipOnly keeps only the whole-clip request: live-segment request counts are timing-bound.
func sgWholeClipOnly(line string) (string, bool) {
	if !strings.Contains(line, "len=256044 ") {
		return "", false
	}
	return sgBackendHit.ReplaceAllString(line, "hit N:"), true
}

func streamScenariosLive(_ *testing.T) []sgScenario {
	speech := pcmPattern(4000, 1000, 3000)
	long := sgInvented
	return []sgScenario{
		{name: "live_pcm_segmented", query: "language=en", backends: sgLiveBackend(), gw: sgLiveCfg, client: sgPCMChunks(speech, "")},
		{name: "live_pcm_segmented_enhance", query: "language=en&enhance=true", backends: sgLiveBackend(), gw: sgLiveCfg,
			inline: sgPP("clean", "m", nil), client: sgPCMChunks(speech, "")},
		{name: "live_pcm_language_changed_at_done", query: "language=en", gw: sgLiveCfg, client: sgPCMChunks(speech, "de"), backendLine: sgWholeClipOnly,
			backends: []goldBackend{gb("small", goldOK("whole clip"))}},
		{name: "live_pcm_short_clip", query: "language=en", backends: sgLiveBackend(), gw: sgLiveCfg, client: sgPCMChunks(pcmPattern(300), "")},
		{name: "live_pcm_invented_text", query: "language=en", gw: sgLiveCfg, client: sgPCMChunks(speech, ""),
			backends: []goldBackend{gb("small", goldOK(long))}},
		{name: "live_pcm_backend_fails_whole_clip_retry", query: "language=en", gw: sgLiveCfg, client: sgPCMChunks(speech, ""),
			backends: sgTwoBackends(func(w http.ResponseWriter, _ *http.Request, hit int) {
				if hit == 1 {
					fmt.Fprint(w, `{"text":"seg1."}`)
					return
				}
				w.WriteHeader(500)
			}, goldOK("fallback text"))},
		{name: "live_pcm_invented_text_short_clip_empty_result", query: "language=en", gw: sgLiveCfg, client: sgPCMChunks(pcmPattern(0, 500, 1500), ""),
			backends: []goldBackend{gb("small", goldOK(sgInvented))}},
		{name: "live_pcm_invented_text_whole_clip_fails", query: "language=en", gw: sgLiveCfg, client: sgPCMChunks(pcmPattern(4000, 1000, 2000), ""),
			backends: []goldBackend{gb("small", func(w http.ResponseWriter, r *http.Request, hit int) {
				if hit == 1 {
					w.WriteHeader(500)
					return
				}
				goldOK(sgInvented)(w, r, hit)
			})}},
		{name: "live_pcm_builtin_preview_no_partials", query: "language=en&preview=cloud", backends: sgLiveBackend(),
			gw: func(g *Gateway) { sgLiveCfg(g); g.live.Preview = true; g.live.PartialEveryMs = 100000 }, client: sgPCMChunks(speech, "")},
		{name: "live_decode_path_feeds_engine", query: "codec=opus", offerOpus: true, backends: sgLiveWAVBackend(), gw: sgLiveCfg,
			pre:    func(t *testing.T) func() { return sgFFmpeg(t, "cat >/dev/null\nhead -c 32000 /dev/zero\n") },
			client: sgOpusClient(buildMinimalOggOpus(48000, 0))},
		{name: "relay_preview_pcm", query: "language=en&preview=cloud", backends: sgOKBackend("x"),
			pre: sgSilentRelay, client: func(c sgClient) { c.audio(3200); c.done() }},
		{name: "nonlive_long_text_for_short_audio_fails", query: "language=en", gw: func(g *Gateway) { g.live.Segmented = true; g.live.PauseMs = 700 },
			backends: []goldBackend{gb("small", goldOK(long))}, client: func(c sgClient) { c.audio(3200); c.done() }},
	}
}

func sgMergeSetup(mergeErr error) func(t *testing.T) func() {
	return sgMergeSetupWith(func() error { return mergeErr })
}

func sgMergeSetupWith(mkErr func() error) func(t *testing.T) func() {
	return func(t *testing.T) func() {
		mergeErr := mkErr()
		waitHandlers(t)
		pm, pg, pi, pd := MergeProcess, OnMerged, OnMergeInputs, mergeDeviceGrace
		mergeDeviceGrace = 150 * time.Millisecond
		MergeProcess = func(_ context.Context, cloud string, phone map[string]string, _ string) (string, string, error) {
			if mergeErr != nil {
				return "", "", mergeErr
			}
			return fmt.Sprintf("merged:%s:%v", cloud, phone), "transcribe", nil
		}
		OnMerged = func(_ context.Context, merged bool) { sgGlobalLog.add("hook OnMerged merged=%v", merged) }
		OnMergeInputs = func(_ context.Context, phones []string, _ time.Duration) {
			sgGlobalLog.add("hook OnMergeInputs phones=%v", phones)
		}
		return func() {
			waitHandlers(t)
			MergeProcess, OnMerged, OnMergeInputs, mergeDeviceGrace = pm, pg, pi, pd
		}
	}
}

var sgGlobalLog *goldLog

func streamScenariosMerge(_ *testing.T) []sgScenario {
	frame := func(c sgClient) {
		c.audio(3200)
		c.done()
		c.text(`{"action":"device_transcript","model":"nemotron","text":"phone text"}`)
	}
	noFrame := func(c sgClient) { c.audio(3200); c.done() }
	q := "language=en&enhance=true&merge=1&phones=nemotron"
	cleanup := sgPP("cleaned", "transcribe", nil)
	mk := func(name, query string, cl func(sgClient), pre func(*testing.T) func(), inline sgPostFn) sgScenario {
		return sgScenario{name: name, query: query, backends: sgOKBackend("cloud text"), client: cl, pre: pre, inline: inline}
	}
	return []sgScenario{
		mk("merge_one_phone_frame", q, frame, sgMergeSetup(nil), cleanup),
		mk("merge_no_phone_frame", q, noFrame, sgMergeSetup(nil), cleanup),
		mk("merge_error_falls_back", q, frame, sgMergeSetup(errors.New("merge down")), cleanup),
		mk("merge_edit_intent_skips_merge", q+"&intent=edit", frame, sgMergeSetup(nil), sgPP("edited", "edit", nil)),
		mk("merge_rejected_cleanup_fallback", q, frame, sgMergeSetupWith(func() error {
			return &MergeRejectedError{Deadline: time.Now().Add(5 * time.Second)}
		}), cleanup),
		mk("merge_rejected_no_budget", q, frame, sgMergeSetupWith(func() error {
			return &MergeRejectedError{Deadline: time.Now().Add(50 * time.Millisecond)}
		}), cleanup),
		mk("merge_enhance_off", "language=en&merge=1", frame, sgMergeSetup(nil), cleanup),
		mk("merge_with_split_enhance_not_merged", q+"&split_enhance=true", frame, sgMergeSetup(nil), cleanup),
		{name: "idle_timeout_then_nothing_more", query: "language=en", backends: sgOKBackend("x"),
			gw:     func(g *Gateway) { g.streamIdleTimeout = 100 * time.Millisecond },
			client: func(c sgClient) { c.pause(400) }},
	}
}

const sgInvented = "Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, " +
	"Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta, Alpha Bravo Charlie Delta"

func sgLiveWAVBackend() []goldBackend {
	b := sgLiveBackend()
	b[0].cfg.NeedsWAV = true
	return b
}

type sgSilentPreview struct {
	ch   chan string
	once sync.Once
}

func (*sgSilentPreview) Send([]byte)               { sgGlobalLog.add("relay send") }
func (p *sgSilentPreview) Interims() <-chan string { return p.ch }
func (p *sgSilentPreview) Stop() {
	p.once.Do(func() { sgGlobalLog.add("relay stop"); close(p.ch) })
}

func sgSilentRelay(t *testing.T) func() {
	pr, ph := PreviewRelay, OnPreviewRelay
	PreviewRelay = func(_ context.Context, lang string) PreviewSession {
		sgGlobalLog.add("relay open lang=%s", lang)
		return &sgSilentPreview{ch: make(chan string)}
	}
	OnPreviewRelay = func(_ context.Context, outcome string) { sgGlobalLog.add("relay outcome %s", outcome) }
	return func() { waitHandlers(t); PreviewRelay, OnPreviewRelay = pr, ph }
}
