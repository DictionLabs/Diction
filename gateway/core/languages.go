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

// cohereSupported is every language the Cohere Transcribe container accepts
// (server/cohere-diction/transcriptions_endpoint.py). It is the backend's
// capability set (Backend.Languages), not a routing decision — which languages
// actually go to Cohere is cloudSTTModels below.
var cohereSupported = map[string]bool{
	"en": true, "de": true, "fr": true, "it": true, "es": true, "pt": true, "el": true,
	"nl": true, "pl": true, "ar": true, "vi": true, "zh": true, "ja": true, "ko": true,
}

// sttSlot names one of the gateway's configured STT model slots. The cloud STT
// model table points at slots, not backend names, so a gateway that leaves a
// slot unset (the community build has no COHERE_MODEL / PARAKEET_V3_MODEL)
// skips that row and routes the language exactly as it did before the table.
type sttSlot int

const (
	slotCohere   sttSlot = iota + 1 // COHERE_MODEL      (prod: cohere-transcribe)
	slotParakeet                    // PARAKEET_V3_MODEL (prod: parakeet-v3)
	slotDefault                     // DEFAULT_MODEL     (prod: canary-v2)
	slotFallback                    // FALLBACK_MODEL    (prod: large-v3-turbo)
)

// cloudSTTModels is the cloud STT model table: the single source of truth for
// which model serves each measured language on cloud batch transcription
// (HTTP /v1/audio/transcriptions, the Diction One socket, and both retry paths
// all go through ModelForLanguage). Only measured languages are listed;
// every other language routes through the EU / non-EU tiers exactly as before.
//
// Measured 2026-10-13..18 on the route-eval lean clip sets (FLEURS + Common
// Voice, ~120 short clips incl. café noise, raw WER; CER for ja/zh/ko), every
// Cara batch model that serves the language, paired bootstrap. A language moves
// off today's model only for >=1 pp, a significant paired difference and no
// meaningful p90 latency cost. Decision record and full numbers:
// .claude/bow/server-stt-routing-2026-10.md.
var cloudSTTModels = map[string]sttSlot{
	// Cohere kept: canary-v2, parakeet-v3 and large-v3-turbo were all worse or
	// within noise (en 4.7 % WER, es 4.3, pt 6.3, fr 9.4, de 6.4, it 4.5, nl 5.8).
	"en": slotCohere, "es": slotCohere, "pt": slotCohere, "fr": slotCohere,
	"de": slotCohere, "it": slotCohere, "nl": slotCohere,

	// Moved 2026-10-18 (raw WER/CER, today's model → table model):
	"pl": slotCohere,   // large-v3-turbo 16.5 → cohere 10.9 (parakeet 10.2 tied; Cohere pins the language and takes My Words)
	"ru": slotFallback, // canary-v2 10.4 → large-v3-turbo 8.1 (parakeet 7.3 tied with turbo; the container's 500 on a `language` field was fixed 2026-10-18 — it now ignores the field)
	"ar": slotCohere,   // large-v3-turbo 18.6 → cohere 8.8
	"vi": slotCohere,   // large-v3-turbo 22.2 → cohere 13.8
	"zh": slotCohere,   // large-v3-turbo 15.5 → cohere 12.0 CER

	// Kept on large-v3-turbo: ja (cohere 11.9 vs 14.1 CER, not significant),
	// ko (cohere worse, 9.3 vs 6.9 CER), hi and tr (no other Cara model serves
	// them). Listed so the table names every measured language.
	"ja": slotFallback, "ko": slotFallback, "hi": slotFallback, "tr": slotFallback,
}

// slotModel returns the model name configured for a slot ("" when unset).
func (g *Gateway) slotModel(s sttSlot) string {
	switch s {
	case slotCohere:
		return g.cohereModel
	case slotParakeet:
		return g.parakeetModel
	case slotDefault:
		return g.defaultModel
	case slotFallback:
		return g.fallbackModel
	}
	return ""
}

// tableModel returns the cloud STT model table's model for lang when that row
// exists, its slot is configured, and the model qualifies (healthy and able to
// serve lang). "" means: no usable table row — route through the tiers.
func (g *Gateway) tableModel(lang string) string {
	slot, ok := cloudSTTModels[lang]
	if !ok {
		return ""
	}
	m := g.slotModel(slot)
	if m == "" || !g.qualifies(m, lang) {
		return ""
	}
	return m
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
// Routing, evaluated in order (when all models are configured):
//
//  1. Cloud STT model table (cloudSTTModels): the measured model for this
//     language, if its slot is configured and the model qualifies (healthy and
//     capable). Otherwise fall through — the tiers below are the fallback chain.
//  2. English (lang=="en" or empty): englishModel (prod: canary-v2).
//  3. EU languages (25): defaultModel (canary-1b-v2), then fallbackModel.
//  4. Non-EU languages: fallbackModel (large-v3-turbo, 99-language Whisper),
//     then defaultModel if it can serve the language.
//
// If englishModel is not configured, tier 2 collapses into tier 3.
// If fallbackModel is not configured, all traffic goes to defaultModel.
// When nothing qualifies, the tier's preferred model is returned anyway.
//
// Every fallback candidate must pass qualifies() (healthy AND capable of the
// language), not health alone. This was added 2026-09 after large-v3-turbo's
// mel-bin bug (see turbo-mel-mismatch-fix-plan.md) left it unhealthy for ~23h;
// the non-EU tier's health-only fallback sent every zh/ja/ko/ar/tr/sr
// dictation to canary-v2, which only covers 25 EU languages and returned 400
// "Unsupported language" 218 times. The check is gated behind
// DICTION_LANG_CAPABILITY_ROUTING (default on) as a rollback path.
// See .claude/bow/stt-failure-rate-2026-09-13.md and
// .claude/bow/server-stt-routing-2026-10.md (the table's measurements).
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

	// Tier 1: cloud STT model table (measured per language)
	if m := g.tableModel(effectiveLang); m != "" {
		return m
	}

	// Tier 2: English (or empty → assume English as most common case)
	if g.englishModel != "" && (lang == "en" || lang == "") {
		if g.health.get(g.englishModel) {
			return g.englishModel
		}
		// englishModel unhealthy — fall through to EU tier
	}

	// Tier 3: EU languages (including English when no englishModel, or as fallback)
	if lang == "" || euLanguages[lang] {
		if g.qualifies(g.defaultModel, effectiveLang) {
			return g.defaultModel
		}
		if g.qualifies(g.fallbackModel, effectiveLang) {
			return g.fallbackModel
		}
		return g.defaultModel
	}

	// Tier 4: Non-EU languages. This is the tier that caused the 2026-09
	// incident: while large-v3-turbo was demoted, health-only fallback sent
	// zh/ja/ko/ar/tr/sr to canary-v2, which returned 400 "Unsupported
	// language" 218 times — canary's coverage is the same 25 EU languages as
	// tier 3, so it was never a real alternative for a non-EU language. The
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

	// cohere_confident: the cloud STT model table sends the dominant language to Cohere,
	// Cohere healthy. Checked before canary_confident so devices converging on such a
	// language get the same model explicit-language requests get. Normalized the same
	// defensive way IsEULanguage is, since dom comes straight from stored device history.
	if dom != "" && g.cohereModel != "" && cloudSTTModels[strings.TrimSpace(strings.ToLower(dom))] == slotCohere && g.health.get(g.cohereModel) {
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
