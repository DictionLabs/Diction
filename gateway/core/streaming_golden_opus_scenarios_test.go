package core

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
)

const (
	sgDecodeOK    = "cat >/dev/null\nhead -c 6400 /dev/zero\n"
	sgDecodeBig   = "cat >/dev/null\nhead -c 5000 /dev/zero\n"
	sgDecodeEmpty = "cat >/dev/null\n"
	sgDecodeDies  = "echo decoder-complaint >&2\nexit 1\n"
)

func sgWAVBackend() []goldBackend {
	return []goldBackend{gb("small", goldOK("opus text"), func(b *Backend) { b.NeedsWAV = true })}
}

func sgOpusClient(frames ...[]byte) func(c sgClient) {
	return func(c sgClient) {
		for _, f := range frames {
			c.bin(f)
		}
		c.done()
	}
}

func sgBigOgg() []byte {
	return append([]byte("OggS"), bytes.Repeat([]byte{1}, 100000)...)
}

func sgBusyPrincipal(_ *testing.T) func() {
	prev := StreamPrincipal
	StreamPrincipal = func(*http.Request) string { return "sg-caller" }
	var releases []func()
	for {
		rel, ok := opusPrincipals.TryAcquire("sg-caller", opusMaxPerPrincipal())
		if !ok {
			break
		}
		releases = append(releases, rel)
	}
	return func() {
		for _, r := range releases {
			r()
		}
		StreamPrincipal = prev
	}
}

func sgBusySemaphore(_ *testing.T) func() {
	sem := getOpusSem()
	n := 0
	for {
		select {
		case sem <- struct{}{}:
			n++
			continue
		default:
		}
		break
	}
	return func() {
		for ; n > 0; n-- {
			<-sem
		}
	}
}

func sgLiveBusySlots(_ *testing.T) func() {
	var releases []func()
	for {
		rel, ok := tryLiveDecodeSlot()
		if !ok {
			break
		}
		releases = append(releases, rel)
	}
	return func() {
		for _, r := range releases {
			r()
		}
	}
}

func streamScenariosOpus(_ *testing.T) []sgScenario {
	ogg1s := buildMinimalOggOpus(48000, 0)
	oggPlain := append([]byte("OggS"), make([]byte, 40)...)
	webm := append([]byte("\x1a\x45\xdf\xa3"), make([]byte, 40)...)
	junk := []byte("not a container at all")
	ffmpeg := func(script string) func(t *testing.T) func() {
		return func(t *testing.T) func() { return sgFFmpeg(t, script) }
	}
	liveCfg := func(g *Gateway) {
		g.live = LiveConfig{PauseMs: 700, MinSegMs: 3000, MaxSegMs: 20000, ShortPauseMs: 300, PartialEveryMs: 100, Segmented: true}
	}
	return []sgScenario{
		{name: "opus_decode_ok", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: ffmpeg(sgDecodeOK), client: sgOpusClient(ogg1s)},
		{name: "opus_decode_without_subprotocol_offer", query: "codec=opus", backends: sgWAVBackend(), pre: ffmpeg(sgDecodeOK), client: sgOpusClient(ogg1s)},
		{name: "opus_decode_webm", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: ffmpeg(sgDecodeOK), client: sgOpusClient(webm)},
		{name: "opus_decode_empty_output", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: ffmpeg(sgDecodeEmpty), client: sgOpusClient(ogg1s)},
		{
			name: "opus_decoded_too_large", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), maxBody: 1000,
			pre: ffmpeg(sgDecodeBig), client: sgOpusClient(ogg1s),
		},
		{name: "opus_ffmpeg_dies_write_error", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: ffmpeg(sgDecodeDies),
			maxBody: 10 << 20, client: sgOpusClient(sgBigOgg(), sgBigOgg(), sgBigOgg())},
		{name: "opus_decode_ffmpeg_start_fails", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(),
			pre: func(_ *testing.T) func() {
				orig := lookupFfmpeg()
				testSetFfmpegPath("/nonexistent/ffmpeg")
				return func() { testSetFfmpegPath(orig) }
			}, client: sgOpusClient(ogg1s)},
		{name: "opus_unknown_magic_decode", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: ffmpeg(sgDecodeOK), client: sgOpusClient(junk)},
		{name: "opus_unknown_magic_passthrough", query: "codec=opus", offerOpus: true, backends: sgOKBackend("x"), client: sgOpusClient(junk)},
		{name: "opus_audio_too_large_compressed", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), maxBody: 100, pre: ffmpeg(sgDecodeOK), client: sgOpusClient(ogg1s)},
		{name: "opus_passthrough_ogg_duration", query: "codec=opus", offerOpus: true, backends: sgOKBackend("passthrough"), client: sgOpusClient(ogg1s)},
		{name: "opus_passthrough_ogg_no_duration", query: "codec=opus", offerOpus: true, backends: sgOKBackend("passthrough"), client: sgOpusClient(oggPlain)},
		{name: "opus_passthrough_webm", query: "codec=opus", offerOpus: true, backends: sgOKBackend("passthrough"), client: sgOpusClient(webm)},
		{name: "opus_passthrough_two_chunks", query: "codec=opus&enhance=true", offerOpus: true, backends: sgOKBackend("passthrough"),
			inline: sgPP("clean", "m", nil), client: sgOpusClient(ogg1s[:30], ogg1s[30:])},
		{name: "opus_no_ffmpeg_decode_backend", query: "codec=opus", backends: sgWAVBackend(), pre: func(_ *testing.T) func() { return sgNoFFmpeg() }, client: sgOpusClient(ogg1s)},
		{name: "opus_no_ffmpeg_client_offers_falls_back_to_pcm", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(),
			pre: func(_ *testing.T) func() { return sgNoFFmpeg() }, client: func(c sgClient) { c.audio(3200); c.done() }},
		{name: "opus_no_ffmpeg_passthrough_backend", query: "codec=opus", backends: sgOKBackend("x"),
			pre: func(_ *testing.T) func() { return sgNoFFmpeg() }, client: sgOpusClient(ogg1s)},
		{name: "opus_offered_but_pcm_codec", query: "language=en", offerOpus: true, backends: sgOKBackend("x"),
			client: func(c sgClient) { c.audio(3200); c.done() }},
		{name: "opus_with_preview_relay_reports_failed", query: "codec=opus&preview=cloud", offerOpus: true, backends: sgOKBackend("x"),
			pre: sgSilentRelay, client: sgOpusClient(ogg1s)},
		{name: "opus_principal_cap", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: func(t *testing.T) func() {
			a, b := sgFFmpeg(t, sgDecodeOK), sgBusyPrincipal(t)
			return func() { b(); a() }
		}, client: sgOpusClient(ogg1s)},
		{name: "opus_semaphore_saturated_5s", query: "codec=opus", offerOpus: true, backends: sgWAVBackend(), pre: func(t *testing.T) func() {
			a, b := sgFFmpeg(t, sgDecodeOK), sgBusySemaphore(t)
			return func() { b(); a() }
		}, client: sgOpusClient(ogg1s)},
		{name: "live_opus_decoder_matches_duration", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: ffmpeg("cat >/dev/null\nhead -c 32000 /dev/zero\n"), client: sgOpusClient(ogg1s)},
		{name: "live_opus_decoder_incomplete_uses_whole_clip", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: ffmpeg("cat >/dev/null\nhead -c 3200 /dev/zero\n"), client: sgOpusClient(ogg1s)},
		{name: "live_opus_decoder_dies", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: ffmpeg(sgDecodeDies), client: sgOpusClient(sgBigOgg(), sgBigOgg())},
		{name: "live_opus_decoder_does_not_start", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: func(_ *testing.T) func() {
				orig := lookupFfmpeg()
				testSetFfmpegPath("/nonexistent/ffmpeg")
				return func() { testSetFfmpegPath(orig) }
			}, client: sgOpusClient(ogg1s)},
		{name: "live_opus_slots_busy", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: func(t *testing.T) func() {
				a, b := sgFFmpeg(t, sgDecodeOK), sgLiveBusySlots(t)
				return func() { b(); a() }
			}, client: sgOpusClient(ogg1s)},
		{name: "live_opus_caller_cap_busy", query: "codec=opus", offerOpus: true, backends: sgLiveBackend(), gw: liveCfg,
			pre: func(t *testing.T) func() {
				a, b := sgFFmpeg(t, sgDecodeOK), sgBusyPrincipal(t)
				return func() { b(); a() }
			}, client: sgOpusClient(ogg1s)},
	}
}

func sgLiveBackend() []goldBackend {
	return []goldBackend{gb("small", func(w http.ResponseWriter, _ *http.Request, hit int) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"seg%d."}`, hit)
	})}
}
