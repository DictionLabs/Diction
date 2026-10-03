package core

import (
	"strings"
)

// AutoDetectSentinel is the `language` field value clients send when the user has
// opted into auto-detect. The gateway routes these requests to a detect-capable
// model and strips the field before forwarding upstream so the model performs
// native language ID instead of being locked to a specific code.
const AutoDetectSentinel = "auto"

// IsAutoDetect reports whether the language string is the auto-detect sentinel.
// Case-insensitive + trimmed so misconfigured clients still hit the auto path.
func IsAutoDetect(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), AutoDetectSentinel)
}

// euLanguages is the set of 25 European language codes supported by both
// Parakeet v3 and Canary-1B-v2. These get the best-accuracy EU model;
// all other languages fall back to Whisper large-v3-turbo (99 languages).
var euLanguages = map[string]bool{
	"en": true, "bg": true, "hr": true, "cs": true, "da": true,
	"nl": true, "et": true, "fi": true, "fr": true, "de": true,
	"el": true, "hu": true, "it": true, "lv": true, "lt": true,
	"mt": true, "pl": true, "pt": true, "ro": true, "sk": true,
	"sl": true, "es": true, "sv": true, "ru": true, "uk": true,
}

// IsEULanguage reports whether the given language code is in the 25-language
// EU set supported by Parakeet v3 and Canary-1B-v2.
func IsEULanguage(lang string) bool {
	return euLanguages[strings.TrimSpace(strings.ToLower(lang))]
}

// cohereLanguages is the set of languages where Cohere Transcribe ties-or-beats
// canary-1b-v2 and is 1.2-6x faster, per the measured eval
// (.claude/COHERE_TRANSCRIBE_EVAL_RESULTS.md, 2026-07-06). Not the full 14
// languages Cohere supports — only the 7 with a clear, measured win. canary-1b-v2
// remains the default/health-fallback model for these (see ModelForLanguage).
var cohereLanguages = map[string]bool{
	"en": true, "es": true, "de": true, "nl": true,
	"fr": true, "pt": true, "it": true,
}

// nonEUModelOverrides routes specific EU-set language codes to fallbackModel
// (large-v3-turbo) instead of defaultModel (canary-1b-v2), for languages where
// measured data shows canary-1b-v2 is clearly the wrong choice. Routing-only —
// these codes stay classified as EU languages everywhere else (IsEULanguage,
// Parakeet's 25-lang claim). Currently just Polish: whisper-turbo (3.0% WER)
// clearly beats both canary-1b-v2 (12.1%) and Cohere (13.8%), per the eval.
var nonEUModelOverrides = map[string]bool{
	"pl": true,
}

// backendServes reports whether the named backend can serve the given
// language. An unresolvable model name is never a candidate. Auto-detect
// (lang == AutoDetectSentinel) requires the backend to perform its own
// language ID — NeedsExplicitLanguage backends (Canary) fail this. A backend
// with a nil Languages set is assumed to serve everything (the Whisper
// models, and any third-party custom backend whose coverage we don't know).
//
// Added 2026-09 so ModelForLanguage's health fallbacks stop offering a
// backend guaranteed to reject the request — see Backend.Languages doc and
// .claude/bow/stt-failure-rate-2026-09-13.md.
func (g *Gateway) backendServes(model, lang string) bool {
	_, backend := g.resolveBackend(model)
	if backend == nil {
		return false
	}
	if IsAutoDetect(lang) {
		return !backend.NeedsExplicitLanguage
	}
	if backend.Languages == nil {
		return true
	}
	return backend.Languages[strings.TrimSpace(strings.ToLower(lang))]
}

// qualifies reports whether model is both healthy and (when capability
// routing is enabled) able to serve lang. This is the single gate every
// fallback branch of ModelForLanguage must pass through — the tier's final
// preferred-model return deliberately does not use it, so a language with no
// qualifying candidate still gets a real error from the right backend
// instead of silently substituting a wrong one.
func (g *Gateway) qualifies(model, lang string) bool {
	if !g.health.get(model) {
		return false
	}
	if !g.langCapabilityRouting {
		return true
	}
	return g.backendServes(model, lang)
}

// ModelForLanguage returns the best model for the given language code.
//
// Routing layers (when all models are configured), evaluated in order:
//
//  1. Cohere tier (en/es/de/nl/fr/pt/it): cohereModel (Cohere Transcribe, measured
//     fastest+most-accurate for these 7 languages). Falls through to tiers 2-4
//     unchanged if cohereModel unset/unhealthy — canary-1b-v2 already covers all
//     7 of these languages via the English/EU tiers below.
//  2. Non-EU override (pl): fallbackModel (large-v3-turbo) — a measured
//     canary-1b-v2 regression fix, independent of the Cohere decision.
//  3. English (lang=="en" or empty): englishModel (canary-qwen-2.5b, best English accuracy)
//  4. EU languages (other 23): defaultModel (canary-1b-v2, multilingual EU)
//  5. Non-EU languages:         fallbackModel (large-v3-turbo, 99-language Whisper)
//
// If englishModel is not configured, tiers 3+4 collapse to defaultModel.
// If fallbackModel is not configured, all traffic goes to defaultModel.
// Health fallback: unhealthy preferred → try next tier. Both unhealthy → preferred anyway.
//
// Every fallback candidate in tiers 2, 4 and 5 must also pass qualifies()
// (healthy AND capable of the language), not health alone. This was added
// 2026-09 after large-v3-turbo's mel-bin bug (see turbo-mel-mismatch-fix-plan.md)
// left it unhealthy for ~23h; tier 5's health-only fallback sent every
// zh/ja/ko/ar/tr/sr dictation to canary-v2, which only covers 25 EU languages
// and returned 400 "Unsupported language" 218 times. The check is gated
// behind DICTION_LANG_CAPABILITY_ROUTING (default on) as a rollback path.
// See .claude/bow/stt-failure-rate-2026-09-13.md.
//
// See .claude/plans/cohere-transcribe-gateway-routing-plan.md and
// .claude/COHERE_TRANSCRIBE_EVAL_RESULTS.md for the measured decisions behind
// layers 1 and 2.
func (g *Gateway) ModelForLanguage(lang string) string {
	if g.fallbackModel == "" {
		return g.defaultModel
	}

	lang = strings.TrimSpace(strings.ToLower(lang))

	// effectiveLang normalizes the empty-string "assume English" convention so
	// it also resolves through the Cohere tier, not just literal "en".
	effectiveLang := lang
	if effectiveLang == "" {
		effectiveLang = "en"
	}

	// Tier 1: Cohere (measured winning languages)
	if g.cohereModel != "" && cohereLanguages[effectiveLang] {
		if g.health.get(g.cohereModel) {
			return g.cohereModel
		}
		// cohereModel unhealthy — fall through; canary-1b-v2 covers these below.
	}

	// Tier 2: non-EU override (e.g. Polish → whisper-turbo)
	if nonEUModelOverrides[effectiveLang] {
		if g.qualifies(g.fallbackModel, effectiveLang) {
			return g.fallbackModel
		}
		if g.qualifies(g.defaultModel, effectiveLang) {
			return g.defaultModel
		}
		return g.fallbackModel
	}

	// Tier 3: English (or empty → assume English as most common case)
	if g.englishModel != "" && (lang == "en" || lang == "") {
		if g.health.get(g.englishModel) {
			return g.englishModel
		}
		// englishModel unhealthy — fall through to EU tier
	}

	// Tier 4: EU languages (including English when no englishModel, or as fallback)
	if lang == "" || euLanguages[lang] {
		if g.qualifies(g.defaultModel, effectiveLang) {
			return g.defaultModel
		}
		if g.qualifies(g.fallbackModel, effectiveLang) {
			return g.fallbackModel
		}
		return g.defaultModel
	}

	// Tier 5: Non-EU languages. This is the tier that caused the 2026-09
	// incident: while large-v3-turbo was demoted, health-only fallback sent
	// zh/ja/ko/ar/tr/sr to canary-v2, which returned 400 "Unsupported
	// language" 218 times — canary's coverage is the same 25 EU languages as
	// tier 4, so it was never a real alternative for a non-EU language. The
	// qualifies() gate below is what fixes that: a candidate here must
	// actually serve the language, not merely be reachable. When nothing
	// qualifies the tier still returns fallbackModel (the preferred model for
	// non-EU traffic) unconditionally — a real 5xx/400 from the right backend
	// is more honest than a guaranteed 400 from the wrong one, and it keeps
	// the demote-and-retry path meaningful. See
	// .claude/bow/stt-failure-rate-2026-09-13.md and
	// DICTION_LANG_CAPABILITY_ROUTING for the rollback path.
	if g.qualifies(g.fallbackModel, effectiveLang) {
		return g.fallbackModel
	}
	if g.qualifies(g.defaultModel, effectiveLang) {
		return g.defaultModel
	}
	return g.fallbackModel
}

// AutoDetectContext carries per-request signals for routing auto-detect requests.
type AutoDetectContext struct {
	DeviceHash string      // sha256, from log entry — may be ""
	Profile    []langEntry // from ProfileStore.GetProfile — nil = no history
}

// AutoDetectResult is the routing decision for language=auto requests.
type AutoDetectResult struct {
	Model            string // upstream model name; "" = no fallback configured
	UpstreamLanguage string // "" = strip language (native auto-LID); non-empty = pass this code (Canary/Cohere)
	// Tier is the granular routing branch — 5 values for rollout observability:
	//   whisper_safe     — no history (cold start or DB unavailable)
	//   whisper_history  — history shows any non-EU language
	//   parakeet_history — history shows EU-only languages
	//   canary_confident — dominant EU lang ≥ minCount obs and ≥ minPct of history
	//   cohere_confident — dominant lang is one of Cohere's measured-winning languages, Cohere healthy
	//
	// ⚠️ Must NOT start with "whisper" — proxy.go's isWhisperTier check gates
	// InjectVerboseJSON + response-side language recording on that prefix.
	Tier string
}

// ModelForAutoDetect picks the upstream model using per-device language history.
// Cold start always goes to Whisper (safe, learns the real language); once enough
// EU observations accumulate the device graduates to Parakeet then Canary.
// Returns an empty AutoDetectResult if no fallback is configured (community single-model setup).
func (g *Gateway) ModelForAutoDetect(ctx AutoDetectContext) AutoDetectResult {
	if g.fallbackModel == "" {
		return AutoDetectResult{}
	}

	minCount := EnvIntOrDefault("DETECT_MIN_COUNT", 5)
	minPct := EnvFloatOrDefault("DETECT_MIN_PCT", 0.90)

	dom := dominantLang(ctx.Profile, minCount, minPct)

	// cohere_confident: dominant language is one of Cohere's measured winners, Cohere healthy.
	// Checked before canary_confident so devices converging on a Cohere-winning language get
	// the same upgrade explicit-language requests get. Normalized the same defensive way
	// IsEULanguage is, since dom comes straight from stored device history.
	if dom != "" && g.cohereModel != "" && cohereLanguages[strings.TrimSpace(strings.ToLower(dom))] && g.health.get(g.cohereModel) {
		return AutoDetectResult{Model: g.cohereModel, UpstreamLanguage: dom, Tier: "cohere_confident"}
	}

	// canary_confident: dominant EU language in history, Canary healthy
	if g.defaultModel != "" && g.health.get(g.defaultModel) {
		if dom != "" && IsEULanguage(dom) {
			return AutoDetectResult{Model: g.defaultModel, UpstreamLanguage: dom, Tier: "canary_confident"}
		}
	}

	// parakeet_history / whisper_history: decide from what we actually know
	allEU, hasHistory := euProfile(ctx.Profile)
	if hasHistory {
		if allEU && g.parakeetModel != "" && g.health.get(g.parakeetModel) {
			return AutoDetectResult{Model: g.parakeetModel, Tier: "parakeet_history"}
		}
		// Non-EU in history, or Parakeet down
		return AutoDetectResult{Model: g.fallbackModel, Tier: "whisper_history"}
	}

	// whisper_safe: no history — cold start
	return AutoDetectResult{Model: g.fallbackModel, Tier: "whisper_safe"}
}

// euProfile reports whether all entries in a non-empty profile are EU languages.
// Returns (allEU, hasEntries).
func euProfile(entries []langEntry) (allEU bool, hasEntries bool) {
	if len(entries) == 0 {
		return false, false
	}
	for _, e := range entries {
		if !IsEULanguage(e.Code) {
			return false, true
		}
	}
	return true, true
}

// dominantLang returns the top language code if it has ≥ minCount observations
// and accounts for ≥ minPct of all observations. Returns "" otherwise.
func dominantLang(entries []langEntry, minCount int, minPct float64) string {
	if len(entries) == 0 {
		return ""
	}
	top := entries[0]
	if top.Count < minCount {
		return ""
	}
	total := 0
	for _, e := range entries {
		total += e.Count
	}
	if total == 0 {
		return ""
	}
	if float64(top.Count)/float64(total) < minPct {
		return ""
	}
	return top.Code
}
