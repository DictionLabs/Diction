package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func gb(name string, handler func(w http.ResponseWriter, r *http.Request, hit int), mod ...func(b *Backend)) goldBackend {
	b := Backend{Name: name}
	for _, m := range mod {
		m(&b)
	}
	return goldBackend{cfg: b, handler: handler}
}

func goldDropConn(w http.ResponseWriter, _ *http.Request, _ int) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func goldTruncated(w http.ResponseWriter, _ *http.Request, _ int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "100")
	_, _ = w.Write([]byte(`{"tex`))
	w.(http.Flusher).Flush()
	panic(http.ErrAbortHandler)
}

func goldBusyDecoder(t *testing.T) func() {
	installFakeFfmpeg(t, "exit 1\n")
	t.Setenv("DICTION_DECODE_CONCURRENCY", "1")
	resetDecodeLimits()
	prev := decodeQueueWait
	decodeQueueWait = 20 * time.Millisecond
	lim := currentDecodeLimits()
	lim.sem <- struct{}{}
	return func() {
		<-lim.sem
		decodeQueueWait = prev
		resetDecodeLimits()
	}
}

func goldBrokenDecoder(t *testing.T) func() {
	installFakeFfmpeg(t, "exit 1\n")
	resetDecodeLimits()
	return resetDecodeLimits
}

var (
	goldEn      = []goldField{{"language", "en"}}
	goldEnAudio = goldReq{fields: goldEn, fileName: "audio.m4a", fileContent: []byte("fake-audio")}
	goldCtxJSON = `{"customWords":[{"word":"Kubernetes"},{"word":""},{"word":"Diction"}],"app":"notes"}`
)

func goldReqWith(mod func(r *goldReq)) goldReq {
	r := goldEnAudio
	mod(&r)
	return r
}

func goldPP(text, mode string, err error) func(context.Context, string, string, string) (string, string, error) {
	return func(context.Context, string, string, string) (string, string, error) { return text, mode, err }
}

func goldScenarios(t *testing.T) []goldScenario {
	e2eKey := goldE2EKey(t)
	two := func(h1, h2 func(w http.ResponseWriter, r *http.Request, hit int)) []goldBackend {
		return []goldBackend{gb("primary", h1), gb("fallback", h2)}
	}
	autoDetect := goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "auto"}} })
	whisperTwo := func(h func(w http.ResponseWriter, r *http.Request, hit int)) []goldBackend {
		return []goldBackend{gb("canary", goldOK("canary text")), gb("whisper", h)}
	}
	return []goldScenario{
		{name: "method_get", backends: two(goldOK("x"), goldOK("y")), req: goldReq{method: http.MethodGet}},
		{name: "body_read_error", backends: two(goldOK("x"), goldOK("y")), req: goldReq{bodyReader: errReader{}}},
		{name: "body_too_large", backends: two(goldOK("x"), goldOK("y")), maxBody: 10, req: goldEnAudio},
		{name: "unsupported_format", backends: two(goldOK("x"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "en"}, {"response_format", "srt"}} })},
		{name: "text_with_e2e_rejected", backends: two(goldOK("x"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) {
				r.fields = []goldField{{"response_format", "text"}}
				r.headers = map[string]string{"X-Diction-E2E": e2eKey}
			})},
		{name: "text_format_ok", backends: two(goldOK("plain words"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "en"}, {"response_format", "text"}} })},
		{name: "wav_duration_reported", backends: two(goldOK("hello"), goldOK("y")),
			req: goldReq{fields: goldEn, fileName: "audio.wav", fileContent: goldWAV(2)}},
		{name: "no_backend_configured", req: goldEnAudio},
		{name: "json_body_not_multipart", backends: two(goldOK("json path"), goldOK("y")),
			req: goldReq{rawBody: []byte(`{"a":1}`), rawCT: "application/json"}},
		{name: "garbage_multipart", backends: two(goldOK("x"), goldOK("y")),
			req: goldReq{rawBody: []byte("garbage"), rawCT: "multipart/form-data; boundary=xyz"}},
		{name: "backend_config_forwarding",
			backends: []goldBackend{gb("primary", goldOK("hello there"), func(b *Backend) {
				b.AuthHeader, b.ForwardModel, b.TargetPath, b.AcceptsCustomWords = "Bearer k", "whisper-1", "/inference", true
			})},
			req: goldReqWith(func(r *goldReq) {
				r.fields = []goldField{{"language", "en"}, {"model", "x"}, {"context", goldCtxJSON}, {"prompt", "p"}}
			})},
		{name: "custom_words_backend_refuses",
			backends: []goldBackend{gb("primary", goldOK("hello there"))},
			req:      goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "en"}, {"context", goldCtxJSON}} })},
		{name: "prompt_echo_stripped_remainder",
			backends: []goldBackend{gb("primary", goldOK("The following words are spelled exactly as shown: Diction. real words"))},
			req:      goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "en"}, {"context", goldCtxJSON}} })},
		{name: "prompt_echo_only",
			backends: []goldBackend{gb("primary", goldOK("The following words are spelled exactly as shown."))},
			req:      goldReqWith(func(r *goldReq) { r.fields = []goldField{{"language", "en"}, {"context", goldCtxJSON}} })},
		{name: "filler_cleaned", backends: two(goldOK("um so uh hello world"), goldOK("y")), req: goldEnAudio},
		{name: "empty_text_passthrough", backends: two(goldOK(""), goldOK("y")), req: goldEnAudio},
		{name: "backend_nonjson_200", backends: two(goldRaw(200, "not json"), goldOK("y")), req: goldEnAudio},
		{name: "backend_404_passthrough", backends: two(goldRaw(404, `{"error":"nope"}`), goldOK("y")), req: goldEnAudio},
		{name: "backend_truncated_body", backends: two(goldTruncated, goldOK("y")), req: goldEnAudio},
		{name: "quirk_stale_content_length_autodetect_whisper_language",
			backends: whisperTwo(func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"text":"hallo welt","language":"de","segments":[1]}`)
			}),
			gw:  goldProfile("dev1", LangEntry{Code: "fr", Count: 2}),
			req: autoDetect},
		{name: "autodetect_canary_confident",
			backends: []goldBackend{gb("canary", func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"text":"hallo welt","language":"de"}`)
			}), gb("whisper", goldOK("w"))},
			gw:  goldProfile("dev1", LangEntry{Code: "de", Count: 10}),
			req: autoDetect},
		{name: "autodetect_no_device", backends: whisperTwo(goldOK("whisper text")), req: autoDetect},
		{name: "autodetect_empty_device_hash",
			backends: whisperTwo(goldOK("whisper text")), gw: goldProfile(""), req: autoDetect},
		{name: "autodetect_5xx_reroutes",
			backends: []goldBackend{gb("canary", goldRaw(500, "{}")), gb("whisper", goldOK("rescued"))},
			gw:       goldProfile("dev1", LangEntry{Code: "de", Count: 10}),
			req:      autoDetect},
		{name: "quirk_duplicate_route_model_header_on_retry", backends: two(goldRaw(500, `{"e":1}`), goldOK("fallback ok")), req: goldEnAudio},
		{name: "retry_5xx_both_fail", backends: two(goldRaw(500, `{"e":1}`), goldRaw(503, `{"e":2}`)), req: goldEnAudio},
		{name: "retry_5xx_no_alternative", backends: []goldBackend{gb("primary", goldRaw(500, `{"e":1}`))}, req: goldEnAudio},
		{name: "retry_target_unresolvable", backends: two(goldRaw(500, `{"e":1}`), goldOK("y")),
			gw: func(g *Gateway) { g.fallbackModel = "ghost"; g.health.set("ghost", true) }, req: goldEnAudio},
		{name: "backend_unreachable", backends: []goldBackend{gb("primary", goldDropConn)}, req: goldEnAudio},
		{name: "client_canceled", backends: two(goldOK("x"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.canceled = true })},
		{name: "hallucination_retry_success", backends: two(goldOK(strings.Repeat("the the ", 40)), goldOK("clean")), req: goldEnAudio},
		{name: "hallucination_no_alternative",
			backends: []goldBackend{gb("primary", goldOK(strings.Repeat("the the ", 40)))}, req: goldEnAudio},
		{name: "hallucination_both",
			backends: two(goldOK(strings.Repeat("the the ", 40)), goldOK(strings.Repeat("the the ", 40))), req: goldEnAudio},
		{name: "postprocess_success", backends: two(goldOK("raw text"), goldOK("y")),
			postProcess: goldPP("Polished.", "cleanup", nil),
			req: goldReqWith(func(r *goldReq) {
				r.query = "enhance=true&intent=cleanup"
				r.fields = []goldField{{"language", "en"}, {"context", goldCtxJSON}}
			})},
		{name: "postprocess_error_skips", backends: two(goldOK("raw text"), goldOK("y")),
			postProcess: goldPP("", "", errors.New("llm down")),
			req:         goldReqWith(func(r *goldReq) { r.query = "enhance=true" })},
		{name: "postprocess_error_canceled", backends: two(goldOK("raw text"), goldOK("y")),
			postProcess: goldPP("", "", context.Canceled),
			req:         goldReqWith(func(r *goldReq) { r.query = "enhance=true" })},
		{name: "postprocess_edit_failure", backends: two(goldOK("make it shorter"), goldOK("y")),
			postProcess: goldPP("", "", errors.New("llm down")),
			req:         goldReqWith(func(r *goldReq) { r.query = "enhance=true&intent=edit" })},
		{name: "postprocess_edit_selected_failure_e2e", backends: two(goldOK("make it shorter"), goldOK("y")),
			postProcess: goldPP("", "", errors.New("llm down")),
			req: goldReqWith(func(r *goldReq) {
				r.query = "enhance=true&intent=edit-selected"
				r.headers = map[string]string{"X-Diction-E2E": e2eKey}
			})},
		{name: "postprocess_auto_edit", backends: two(goldOK("shorten this"), goldOK("y")),
			postProcess: goldPP("short", "edit", nil),
			intentHook: func(_ context.Context, _ *http.Request, _, _, _ string) (string, bool) {
				return "edit", true
			},
			req: goldReqWith(func(r *goldReq) { r.query = "enhance=true" })},
		{name: "postprocess_auto_edit_e2e", backends: two(goldOK("shorten this"), goldOK("y")),
			postProcess: goldPP("short", "edit", nil),
			intentHook: func(_ context.Context, _ *http.Request, _, _, _ string) (string, bool) {
				return "edit", true
			},
			req: goldReqWith(func(r *goldReq) {
				r.query = "enhance=true"
				r.headers = map[string]string{"X-Diction-E2E": e2eKey}
			})},
		{name: "postprocess_auto_flag_other_mode", backends: two(goldOK("shorten this"), goldOK("y")),
			postProcess: goldPP("text", "cleanup", nil),
			intentHook: func(_ context.Context, _ *http.Request, _, _, _ string) (string, bool) {
				return "cleanup", true
			},
			req: goldReqWith(func(r *goldReq) { r.query = "enhance=true" })},
		{name: "postprocess_text_format", backends: two(goldOK("raw"), goldOK("y")),
			postProcess: goldPP("Polished text", "cleanup", nil),
			req: goldReqWith(func(r *goldReq) {
				r.query = "enhance=true"
				r.fields = []goldField{{"language", "en"}, {"response_format", "text"}}
			})},
		{name: "enhance_without_postprocess", backends: two(goldOK("raw"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.query = "enhance=true" })},
		{name: "e2e_success", backends: two(goldOK("secret words"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.headers = map[string]string{"X-Diction-E2E": e2eKey} })},
		{name: "e2e_bad_key_falls_back", backends: two(goldOK("secret words"), goldOK("y")),
			req: goldReqWith(func(r *goldReq) { r.headers = map[string]string{"X-Diction-E2E": "!!notakey"} })},
		{name: "decode_busy", pre: goldBusyDecoder,
			backends: []goldBackend{gb("primary", goldOK("x"), func(b *Backend) { b.NeedsWAV = true })}, req: goldEnAudio},
		{name: "decode_busy_on_retry", pre: goldBusyDecoder,
			backends: []goldBackend{gb("primary", goldRaw(500, "{}")), gb("wav", goldOK("x"), func(b *Backend) { b.NeedsWAV = true })},
			req:      goldEnAudio},
		{name: "decode_failure_primary", pre: goldBrokenDecoder,
			backends: []goldBackend{gb("primary", goldOK("x"), func(b *Backend) { b.NeedsWAV = true })}, req: goldEnAudio},
		{name: "decode_failure_on_retry_uses_original_body", pre: goldBrokenDecoder,
			backends: []goldBackend{gb("primary", goldRaw(500, "{}")), gb("wav", goldOK("retry text"), func(b *Backend) { b.NeedsWAV = true })},
			req:      goldEnAudio},
		{name: "retry_hallucination_second_backend_5xx",
			backends: []goldBackend{gb("primary", goldOK(strings.Repeat("the the ", 40))), gb("fallback", goldRaw(500, "{}"))},
			req:      goldEnAudio},
	}
}
