package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const sgCtxJSON = `{"app":"notes","customWords":[{"word":"Kubernetes"},{"word":"Diction"}]}`

func sgTwoBackends(primary, fallback func(w http.ResponseWriter, r *http.Request, hit int)) []goldBackend {
	return []goldBackend{
		gb("small", primary, func(b *Backend) { b.AcceptsCustomWords = true }),
		gb("fallback", fallback),
	}
}

func sgSimple(name, query string, client func(c sgClient)) sgScenario {
	return sgScenario{name: name, query: query, backends: sgOKBackend("hello world"), client: client}
}

func sgPP(text, mode string, err error) sgPostFn {
	return func(context.Context, string, string, string) (string, string, error) { return text, mode, err }
}

func sgAutoIntent(intent string, auto bool) func(context.Context, *http.Request, string, string, string) (string, bool) {
	return func(context.Context, *http.Request, string, string, string) (string, bool) { return intent, auto }
}

func streamScenarios(t *testing.T) []sgScenario {
	var all []sgScenario
	all = append(all, streamScenariosBasic(t)...)
	all = append(all, streamScenariosFailures(t)...)
	all = append(all, streamScenariosEnhance(t)...)
	all = append(all, streamScenariosOpus(t)...)
	all = append(all, streamScenariosLive(t)...)
	all = append(all, streamScenariosMerge(t)...)
	return all
}

func streamScenariosBasic(_ *testing.T) []sgScenario {
	pcm := func(c sgClient) { c.audio(3200); c.audio(3200); c.done() }
	withCtx := func(first string) func(c sgClient) {
		return func(c sgClient) { c.text(first); c.audio(3200); c.done() }
	}
	return []sgScenario{
		sgSimple("pcm_happy", "language=en", pcm),
		sgSimple("pcm_no_language", "", pcm),
		sgSimple("pcm_codec_explicit", "language=en&codec=pcm", pcm),
		sgSimple("pcm_context_json_first", "language=en", withCtx(sgCtxJSON)),
		sgSimple("pcm_context_not_json", "language=en", withCtx("plain words, not json")),
		sgSimple("pcm_context_after_audio_dropped", "language=en", func(c sgClient) {
			c.audio(3200)
			c.text(sgCtxJSON)
			c.done()
		}),
		sgSimple("pcm_second_context_ignored", "language=en", func(c sgClient) {
			c.text(`{"app":"first"}`)
			c.text(`{"app":"second"}`)
			c.audio(3200)
			c.done()
		}),
		sgSimple("pcm_other_action_ignored", "language=en", func(c sgClient) {
			c.text(`{"action":"ping"}`)
			c.text(`{"app":"after-ping"}`)
			c.audio(3200)
			c.done()
		}),
		sgSimple("pcm_done_language_override", "language=en", func(c sgClient) {
			c.audio(3200)
			c.doneLang("de")
		}),
		{
			name: "pcm_custom_words_to_accepting_backend", query: "language=en",
			backends: sgTwoBackends(goldOK("hello"), goldOK("fb")), client: withCtx(sgCtxJSON),
		},
		{
			name: "autodetect_profile", query: "language=auto", backends: sgOKBackend("hello"),
			gw: goldProfile("dev1", LangEntry{Code: "en", Count: 9}),
			client: func(c sgClient) {
				c.audio(3200)
				c.doneLang("de")
			},
		},
		{
			name: "autodetect_two_backends_no_profile", query: "language=auto", backends: sgTwoBackends(goldOK("canary"), goldOK("whisper")),
			client: pcm,
		},
		{
			name: "autodetect_two_backends_confident_eu", query: "language=auto", backends: sgTwoBackends(goldOK("canary"), goldOK("whisper")),
			gw: goldProfile("dev1", LangEntry{Code: "en", Count: 9}), client: pcm,
		},
		{
			name: "autodetect_two_backends_history_non_eu", query: "language=auto", backends: sgTwoBackends(goldOK("canary"), goldOK("whisper")),
			gw: goldProfile("dev1", LangEntry{Code: "ja", Count: 2}), client: pcm,
		},
		{
			name: "autodetect_primary_fails_retry_on_fallback", query: "language=auto", backends: sgTwoBackends(goldRaw(500, "x"), goldOK("whisper")),
			gw: goldProfile("dev1", LangEntry{Code: "en", Count: 9}), client: pcm,
		},
		{name: "autodetect_no_profile", query: "language=auto", backends: sgOKBackend("hello"), client: pcm},
		sgSimple("no_audio", "language=en", func(c sgClient) { c.done() }),
		sgSimple("no_audio_context_only", "language=en", func(c sgClient) { c.text(sgCtxJSON); c.done() }),
		{
			name: "audio_too_large", query: "language=en", backends: sgOKBackend("x"), maxBody: 1000,
			client: func(c sgClient) { c.audio(600); c.audio(600); c.done() },
		},
		{
			name: "idle_timeout", query: "language=en", backends: sgOKBackend("x"),
			gw:     func(g *Gateway) { g.streamIdleTimeout = 150 * 1000000 },
			client: func(c sgClient) { c.audio(3200); c.pause(600) },
		},
		sgSimple("client_abort_after_audio", "language=en", func(c sgClient) { c.audio(3200); c.pause(50); c.abort() }),
		sgSimple("client_abort_immediately", "language=en", func(c sgClient) { c.abort() }),
		{name: "unsupported_codec", query: "codec=flac", backends: sgOKBackend("x")},
		{name: "plain_get_accept_fails", query: "language=en", plainGET: true, backends: sgOKBackend("x")},
		{
			name: "route_fail_unhealthy", query: "language=en", backends: sgOKBackend("x"),
			gw: func(g *Gateway) { g.health.set("small", false) },
		},
		{
			name: "route_log_with_fallback_model", query: "language=en", backends: sgTwoBackends(goldOK("a"), goldOK("b")),
			client: pcm,
		},
	}
}

func streamScenariosFailures(_ *testing.T) []sgScenario {
	pcm := func(c sgClient) { c.audio(3200); c.done() }
	mk := func(name string, bs []goldBackend) sgScenario {
		return sgScenario{name: name, query: "language=en", backends: bs, client: pcm}
	}
	return []sgScenario{
		mk("backend_500_no_fallback", sgOKBackendRaw(500, `{"error":"boom"}`)),
		mk("backend_400_no_fallback", sgOKBackendRaw(400, `{"error":"bad"}`)),
		mk("backend_drop_no_fallback", []goldBackend{gb("small", goldDropConn)}),
		mk("backend_truncated_json", []goldBackend{gb("small", goldTruncated)}),
		mk("backend_500_then_fallback_ok", sgTwoBackends(goldRaw(500, "x"), goldOK("from fallback"))),
		mk("backend_drop_then_fallback_ok", sgTwoBackends(goldDropConn, goldOK("from fallback"))),
		mk("backend_truncated_then_fallback_ok", sgTwoBackends(goldTruncated, goldOK("from fallback"))),
		mk("backend_500_fallback_hallucinates", sgTwoBackends(goldRaw(500, "x"), goldOK("no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no no"))),
		mk("backend_500_fallback_drops", sgTwoBackends(goldRaw(500, "x"), goldDropConn)),
		mk("backend_500_fallback_400", sgTwoBackends(goldRaw(500, "x"), goldRaw(400, "y"))),
		mk("backend_500_both_fail", sgTwoBackends(goldRaw(500, "x"), goldRaw(503, "y"))),
		mk("backend_400_fallback_untouched", sgTwoBackends(goldRaw(400, "x"), goldOK("fb"))),
		mk("hallucination_no_fallback", sgOKBackend("the the the the the the the the the the the the the the the the the the the the the the the the the the the")),
		mk("hallucination_fallback_clean", sgTwoBackends(goldOK("go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go go"), goldOK("clean"))),
		mk("empty_text_result", sgOKBackend("")),
	}
}

func sgOKBackendRaw(status int, body string) []goldBackend {
	return []goldBackend{gb("small", goldRaw(status, body))}
}

func streamScenariosEnhance(_ *testing.T) []sgScenario {
	pcm := func(c sgClient) { c.text(sgCtxJSON); c.audio(3200); c.done() }
	mk := func(name, query string, inline, post sgPostFn, hook func(context.Context, *http.Request, string, string, string) (string, bool)) sgScenario {
		return sgScenario{name: name, query: "language=en&" + query, backends: sgTwoBackends(goldOK("raw text"), goldOK("fb")),
			inline: inline, post: post, intentHook: hook, client: pcm}
	}
	ok := sgPP("clean text", "transcribe", nil)
	fail := sgPP("", "", errors.New("llm down"))
	return []sgScenario{
		mk("enhance_ok", "enhance=true", ok, nil, nil),
		mk("enhance_without_postprocess", "enhance=true", nil, nil, nil),
		mk("postprocess_but_enhance_off", "", ok, nil, nil),
		mk("enhance_error_raw_fallback", "enhance=true", fail, nil, nil),
		mk("enhance_edit_intent_ok", "enhance=true&intent=edit", sgPP("edited", "edit", nil), nil, nil),
		mk("enhance_edit_intent_fails", "enhance=true&intent=edit", fail, nil, nil),
		mk("enhance_edit_selected_fails", "enhance=true&intent=edit_selected", fail, nil, nil),
		mk("auto_edit_ok", "enhance=true", sgPP("edited", "edit", nil), nil, sgAutoIntent("edit", true)),
		mk("auto_edit_fails", "enhance=true", fail, nil, sgAutoIntent("edit", true)),
		mk("auto_edit_mode_not_edit", "enhance=true", sgPP("x", "transcribe", nil), nil, sgAutoIntent("edit", true)),
		mk("auto_transcribe_resolved", "enhance=true", ok, nil, sgAutoIntent("transcribe", false)),
		mk("split_ok", "enhance=true&split_enhance=true", ok, nil, nil),
		mk("split_post_delivery_used", "enhance=true&split_enhance=true", sgPP("inline", "m", nil), sgPP("post", "m", nil), nil),
		mk("split_error", "enhance=true&split_enhance=true", fail, nil, nil),
		mk("split_edit_intent_single_frame", "enhance=true&split_enhance=true&intent=edit", sgPP("edited", "edit", nil), nil, nil),
		mk("split_auto_edit", "enhance=true&split_enhance=true", sgPP("edited", "edit", nil), nil, sgAutoIntent("edit", true)),
		mk("split_enhance_without_enhance", "split_enhance=true", ok, nil, nil),
		mk("split_empty_text", "enhance=true&split_enhance=true", ok, nil, nil),
	}
}
