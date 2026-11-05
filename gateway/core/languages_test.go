package core

import (
	"strings"
	"testing"
)

func TestIsEULanguage(t *testing.T) {
	tests := []struct {
		lang string
		want bool
	}{
		{"en", true},
		{"cs", true},
		{"fr", true},
		{"ja", false},
		{"zh", false},
		{"ar", false},
		{"", false},
		{"EN", true},   // case-insensitive
		{" en ", true}, // trimmed
	}
	for _, tt := range tests {
		if got := IsEULanguage(tt.lang); got != tt.want {
			t.Errorf("IsEULanguage(%q) = %v, want %v", tt.lang, got, tt.want)
		}
	}
}

func TestModelForLanguage_NoFallback(t *testing.T) {
	// Self-hosted: no fallback model configured — always returns default.
	g := &Gateway{
		defaultModel:  "small",
		fallbackModel: "",
		health:        newHealthState(),
	}
	if got := g.ModelForLanguage("en"); got != "small" {
		t.Errorf("got %q, want small", got)
	}
	if got := g.ModelForLanguage("ja"); got != "small" {
		t.Errorf("got %q, want small", got)
	}
}

func TestModelForLanguage_EULanguage(t *testing.T) {
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)

	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2", got)
	}
	if got := g.ModelForLanguage("cs"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2", got)
	}
}

func TestModelForLanguage_NonEULanguage(t *testing.T) {
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)

	if got := g.ModelForLanguage("ja"); got != "large-v3-turbo" {
		t.Errorf("got %q, want large-v3-turbo", got)
	}
	if got := g.ModelForLanguage("zh"); got != "large-v3-turbo" {
		t.Errorf("got %q, want large-v3-turbo", got)
	}
}

func TestModelForLanguage_EmptyLanguage(t *testing.T) {
	// Empty language defaults to the EU model (default model).
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)

	if got := g.ModelForLanguage(""); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2", got)
	}
}

func TestModelForLanguage_HealthFallback_DefaultDown(t *testing.T) {
	// Default model is down, EU language → fall back to large-v3-turbo.
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", false)
	g.health.set("large-v3-turbo", true)

	if got := g.ModelForLanguage("en"); got != "large-v3-turbo" {
		t.Errorf("got %q, want large-v3-turbo (health fallback)", got)
	}
}

// languageCapableTestGateway builds a Gateway with real Backend entries (as
// opposed to the bare-struct Gateways above), so backendServes/qualifies has
// something to resolve. canary-v2 declares the 25 EU languages and requires
// an explicit code (mirrors DefaultBackends()); large-v3-turbo declares no
// Languages (nil = serves everything), matching Whisper's real coverage.
func languageCapableTestGateway() *Gateway {
	return &Gateway{
		backends: []Backend{
			{Name: "canary-v2", Aliases: []string{"canary-v2"}, Languages: langSet(euLanguages), NeedsExplicitLanguage: true},
			{Name: "large-v3-turbo", Aliases: []string{"large-v3-turbo"}},
		},
		defaultModel:          "canary-v2",
		fallbackModel:         "large-v3-turbo",
		health:                newHealthState(),
		langCapabilityRouting: true,
	}
}

// TestModelForLanguage_HealthFallback_TurboDown previously asserted the bug
// this plan fixes: with large-v3-turbo down, a non-EU language fell through
// to canary-v2, which cannot serve it and would return a guaranteed 400 (see
// .claude/bow/stt-failure-rate-2026-09-13.md — 218 such 400s measured live).
// The fix: canary-v2 is never offered for a language outside its declared
// set, so the tier keeps returning the preferred (still-demoted) model
// instead of a wrong-but-healthy one.
func TestModelForLanguage_HealthFallback_TurboDown(t *testing.T) {
	g := languageCapableTestGateway()
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	for _, lang := range []string{"ja", "zh", "ko", "ar"} {
		if got := g.ModelForLanguage(lang); got != "large-v3-turbo" {
			t.Errorf("ModelForLanguage(%q) = %q, want large-v3-turbo (canary-v2 cannot serve this language even though it is healthy)", lang, got)
		}
	}
}

// TestModelForLanguage_HealthFallback_TurboDown_PolishStillFallsBack proves
// the capability check is a filter, not a blanket "never fall back": Polish
// is EU-set (the cloud STT model table routes it away from canary normally), and canary-v2
// genuinely does serve it, so with turbo down the fallback to canary must
// still fire.
func TestModelForLanguage_HealthFallback_TurboDown_PolishStillFallsBack(t *testing.T) {
	g := languageCapableTestGateway()
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("pl"); got != "canary-v2" {
		t.Errorf("ModelForLanguage(pl) = %q, want canary-v2 (it does serve Polish)", got)
	}
}

// TestModelForLanguage_HealthFallback_TurboDown_AutoNeverGoesToCanary proves
// language=auto is treated as requiring native language ID: canary cannot
// perform its own LID (NeedsExplicitLanguage), so even though it is healthy
// and turbo is not, auto must not be routed to it. This is what prevents the
// literal "auto" sentinel from reaching canary and 400ing (measured 54 times
// before this fix).
func TestModelForLanguage_HealthFallback_TurboDown_AutoNeverGoesToCanary(t *testing.T) {
	g := languageCapableTestGateway()
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("auto"); got != "large-v3-turbo" {
		t.Errorf("ModelForLanguage(auto) = %q, want large-v3-turbo (canary cannot do its own language ID)", got)
	}
}

// TestModelForLanguage_CapabilityRouting_KillSwitch asserts the
// DICTION_LANG_CAPABILITY_ROUTING=false rollback path: with capability
// routing disabled, behaviour reverts to health-only fallback (the old,
// buggy-for-non-EU-languages behaviour), so the revert is known to work
// before it is ever needed.
func TestModelForLanguage_CapabilityRouting_KillSwitch(t *testing.T) {
	g := languageCapableTestGateway()
	g.langCapabilityRouting = false
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("ja"); got != "canary-v2" {
		t.Errorf("ModelForLanguage(ja) with capability routing off = %q, want canary-v2 (old health-only behaviour)", got)
	}
}

func TestModelForLanguage_BothUnhealthy(t *testing.T) {
	// Both down — returns preferred anyway (might recover).
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", false)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2 (preferred even when unhealthy)", got)
	}
	if got := g.ModelForLanguage("ja"); got != "large-v3-turbo" {
		t.Errorf("got %q, want large-v3-turbo (preferred even when unhealthy)", got)
	}
}

func TestEULanguageSet_Exact25(t *testing.T) {
	// Guard against drift — the set must contain exactly 25 languages.
	if got := len(euLanguages); got != 25 {
		t.Errorf("euLanguages has %d entries, want 25", got)
	}
}

// --- Three-tier routing: englishModel ---

func testGatewayThreeTier() *Gateway {
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		englishModel:  "canary-qwen",
		health:        newHealthState(),
	}
	g.health.set("canary-qwen", true)
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)
	return g
}

func TestModelForLanguage_English_UsesEnglishModel(t *testing.T) {
	g := testGatewayThreeTier()
	if got := g.ModelForLanguage("en"); got != "canary-qwen" {
		t.Errorf("got %q, want canary-qwen", got)
	}
}

func TestModelForLanguage_Empty_UsesEnglishModel(t *testing.T) {
	// Empty language → assume English (most common case).
	g := testGatewayThreeTier()
	if got := g.ModelForLanguage(""); got != "canary-qwen" {
		t.Errorf("got %q, want canary-qwen", got)
	}
}

func TestModelForLanguage_OtherEU_UsesDefaultModel(t *testing.T) {
	// "pl" deliberately excluded here — see cloudSTTModels / the Polish
	// routing fix (TestModelForLanguage_Polish_HealthyFallback), which routes it
	// to large-v3-turbo instead of canary-v2 regardless of Cohere configuration.
	g := testGatewayThreeTier()
	for _, lang := range []string{"cs", "de", "fr", "uk"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2", lang, got)
		}
	}
}

func TestModelForLanguage_NonEU_UsesFallback(t *testing.T) {
	g := testGatewayThreeTier()
	for _, lang := range []string{"ja", "zh", "ar", "ko"} {
		if got := g.ModelForLanguage(lang); got != "large-v3-turbo" {
			t.Errorf("lang=%q: got %q, want large-v3-turbo", lang, got)
		}
	}
}

func TestModelForLanguage_EnglishModelDown_FallsToCanary(t *testing.T) {
	g := testGatewayThreeTier()
	g.health.set("canary-qwen", false)
	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2 (english model down)", got)
	}
}

func TestModelForLanguage_EnglishModelDown_EmptyLang_FallsToCanary(t *testing.T) {
	g := testGatewayThreeTier()
	g.health.set("canary-qwen", false)
	if got := g.ModelForLanguage(""); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2 (english model down)", got)
	}
}

func TestModelForLanguage_NoEnglishModel_EnUsesDefault(t *testing.T) {
	// Without englishModel, English routes to defaultModel like any other EU language.
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		englishModel:  "",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)
	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2", got)
	}
}

// --- IsAutoDetect ---

func TestIsAutoDetect(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"auto", true},
		{"AUTO", true},
		{" auto ", true},
		{"Auto", true},
		{"", false},
		{"en", false},
		{"en,fr", false}, // CSV is no longer special-cased
		{"automatic", false},
	}
	for _, tt := range tests {
		if got := IsAutoDetect(tt.in); got != tt.want {
			t.Errorf("IsAutoDetect(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// --- ModelForAutoDetect (four-tier history-based routing) ---

func testAutoDetectGateway() *Gateway {
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		parakeetModel: "parakeet-v3",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("parakeet-v3", true)
	g.health.set("large-v3-turbo", true)
	return g
}

func TestModelForAutoDetect_NoFallbackConfigured(t *testing.T) {
	// Community single-model setup: no fallback → empty result, caller uses ModelForLanguage.
	g := &Gateway{
		defaultModel:  "small",
		fallbackModel: "",
		health:        newHealthState(),
	}
	result := g.ModelForAutoDetect(AutoDetectContext{})
	if result.Model != "" || result.Tier != "" {
		t.Errorf("no fallback: got model=%q tier=%q, want empty", result.Model, result.Tier)
	}
}

func TestModelForAutoDetect_WhisperSafe_NoHistory(t *testing.T) {
	// Cold start: no profile → whisper_safe → Whisper, no explicit language.
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_safe" || result.UpstreamLanguage != "" {
		t.Errorf("got model=%q tier=%q lang=%q, want large-v3-turbo/whisper_safe/\"\"",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_WhisperHistory_NonEU(t *testing.T) {
	// Non-EU language in history → whisper_history → Whisper.
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "zh", Count: 3}, {Code: "cs", Count: 2}},
	})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_history" {
		t.Errorf("got model=%q tier=%q, want large-v3-turbo/whisper_history", result.Model, result.Tier)
	}
}

func TestModelForAutoDetect_ParakeetHistory_AllEU(t *testing.T) {
	// All EU languages, no dominant → parakeet_history → Parakeet.
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "cs", Count: 3}, {Code: "sk", Count: 2}},
	})
	if result.Model != "parakeet-v3" || result.Tier != "parakeet_history" || result.UpstreamLanguage != "" {
		t.Errorf("got model=%q tier=%q lang=%q, want parakeet-v3/parakeet_history/\"\"",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_CanaryConfident_DominantEU(t *testing.T) {
	// ≥5 observations, ≥90% one EU language → canary_confident → Canary with explicit lang.
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" || result.UpstreamLanguage != "cs" {
		t.Errorf("got model=%q tier=%q lang=%q, want canary-v2/canary_confident/cs",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_CanaryDown_FallsToParakeet(t *testing.T) {
	// canary_confident device: Canary down → parakeet_history.
	g := testAutoDetectGateway()
	g.health.set("canary-v2", false)
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "parakeet-v3" || result.Tier != "parakeet_history" {
		t.Errorf("canary down: got model=%q tier=%q, want parakeet-v3/parakeet_history",
			result.Model, result.Tier)
	}
}

func TestModelForAutoDetect_ParakeetDown_FallsToWhisper(t *testing.T) {
	// All-EU profile but Parakeet down → whisper_history.
	g := testAutoDetectGateway()
	g.health.set("parakeet-v3", false)
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "cs", Count: 3}, {Code: "sk", Count: 2}},
	})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_history" {
		t.Errorf("parakeet down: got model=%q tier=%q, want large-v3-turbo/whisper_history",
			result.Model, result.Tier)
	}
}

// --- Cohere Transcribe routing (7 measured-winning languages + Polish fix) ---

// testGatewayWithCohere mirrors production config: englishModel == defaultModel
// == "canary-v2", plus cohereModel wired in as the new top tier.
func testGatewayWithCohere() *Gateway {
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		englishModel:  "canary-v2",
		cohereModel:   "cohere-transcribe",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)
	g.health.set("cohere-transcribe", true)
	return g
}

func TestModelForLanguage_CohereLanguages_Healthy(t *testing.T) {
	g := testGatewayWithCohere()
	for _, lang := range []string{"en", "es", "de", "nl", "fr", "pt", "it"} {
		if got := g.ModelForLanguage(lang); got != "cohere-transcribe" {
			t.Errorf("lang=%q: got %q, want cohere-transcribe", lang, got)
		}
	}
}

func TestModelForLanguage_CohereLanguages_EmptyString_ResolvesAsEnglish(t *testing.T) {
	// Load-bearing new behavior: effectiveLang normalization must route "" through
	// the Cohere tier the same as literal "en".
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage(""); got != "cohere-transcribe" {
		t.Errorf("got %q, want cohere-transcribe", got)
	}
}

func TestModelForLanguage_CohereUnhealthy_FallsToExistingTiers(t *testing.T) {
	g := testGatewayWithCohere()
	g.health.set("cohere-transcribe", false)
	// English falls through to englishModel tier == canary-v2.
	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("en: got %q, want canary-v2 (cohere down)", got)
	}
	// Other Cohere languages fall through to the EU tier == canary-v2.
	for _, lang := range []string{"es", "de", "nl", "fr", "pt", "it"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (cohere down)", lang, got)
		}
	}
}

// Polish moved off large-v3-turbo to Cohere on 2026-10-18 (16.5 → 10.9 % WER,
// .claude/bow/server-stt-routing-2026-10.md). With Cohere down it falls back
// through the EU tier: canary-v2 (16.6 %, tied with turbo), then turbo.
func TestModelForLanguage_Polish_UsesCohere(t *testing.T) {
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage("pl"); got != "cohere-transcribe" {
		t.Errorf("pl: got %q, want cohere-transcribe", got)
	}
}

func TestModelForLanguage_Polish_CohereDown_UsesEUTier(t *testing.T) {
	g := testGatewayWithCohere()
	g.health.set("cohere-transcribe", false)
	if got := g.ModelForLanguage("pl"); got != "canary-v2" {
		t.Errorf("pl (cohere down): got %q, want canary-v2", got)
	}
	g.health.set("canary-v2", false)
	if got := g.ModelForLanguage("pl"); got != "large-v3-turbo" {
		t.Errorf("pl (cohere + canary down): got %q, want large-v3-turbo", got)
	}
}

func TestModelForLanguage_EUOnlyAndGreek_UnchangedOnCanary(t *testing.T) {
	// Full regression surface: every EU-set code Cohere doesn't support, plus
	// Greek (technically Cohere-supported but excluded — no measured data),
	// must still resolve to canary-1b-v2 exactly as before this change.
	g := testGatewayWithCohere()
	untouched := []string{
		"bg", "hr", "cs", "da", "et", "fi", "hu", "lt", "lv",
		"mt", "ro", "sk", "sl", "sv", "uk", "el",
	}
	for _, lang := range untouched {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (unaffected by Cohere rollout)", lang, got)
		}
	}
}

// Russian moved off canary-v2 to large-v3-turbo on 2026-10-18 (10.4 → 8.1 % WER);
// with turbo down it falls back to canary-v2 through the EU tier.
func TestModelForLanguage_Russian_UsesTurbo_CanaryWhenDown(t *testing.T) {
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage("ru"); got != "large-v3-turbo" {
		t.Errorf("ru: got %q, want large-v3-turbo", got)
	}
	g.health.set("large-v3-turbo", false)
	if got := g.ModelForLanguage("ru"); got != "canary-v2" {
		t.Errorf("ru (turbo down): got %q, want canary-v2", got)
	}
}

func TestModelForLanguage_NonEU_StillUsesFallback(t *testing.T) {
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage("ja"); got != "large-v3-turbo" {
		t.Errorf("ja: got %q, want large-v3-turbo (unchanged)", got)
	}
}

func TestModelForLanguage_CohereModelUnset_KillSwitch(t *testing.T) {
	// COHERE_MODEL unset (cohereModel == "") must fully disable the Cohere tier —
	// every one of the 7 languages reverts to today's canary-1b-v2 routing.
	g := testGatewayWithCohere()
	g.cohereModel = ""
	for _, lang := range []string{"en", "es", "de", "nl", "fr", "pt", "it"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (cohere kill switch)", lang, got)
		}
	}
}

// --- ModelForAutoDetect: cohere_confident tier ---

func testAutoDetectGatewayWithCohere() *Gateway {
	g := testAutoDetectGateway()
	g.cohereModel = "cohere-transcribe"
	g.health.set("cohere-transcribe", true)
	return g
}

func TestModelForAutoDetect_CohereConfident_DominantCohereLang(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "es", Count: 5}},
	})
	if result.Model != "cohere-transcribe" || result.Tier != "cohere_confident" || result.UpstreamLanguage != "es" {
		t.Errorf("got model=%q tier=%q lang=%q, want cohere-transcribe/cohere_confident/es",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
	if strings.HasPrefix(result.Tier, "whisper") {
		t.Errorf("tier %q must not start with \"whisper\" (isWhisperTier footgun)", result.Tier)
	}
}

func TestModelForAutoDetect_CohereDown_FallsToCanaryConfident(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	g.health.set("cohere-transcribe", false)
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "es", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" || result.UpstreamLanguage != "es" {
		t.Errorf("cohere down: got model=%q tier=%q lang=%q, want canary-v2/canary_confident/es",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_DominantNonCohereEU_StillCanaryConfident(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []langEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" {
		t.Errorf("got model=%q tier=%q, want canary-v2/canary_confident", result.Model, result.Tier)
	}
}

// --- Cloud STT model table (cloudSTTModels) ---

// prodTableGateway mirrors production: the real DefaultBackends (so capability
// checks see each backend's true language set), every slot configured, all
// healthy, capability routing on.
func prodTableGateway() *Gateway {
	g := &Gateway{
		backends:              DefaultBackends(),
		defaultModel:          "canary-v2",
		fallbackModel:         "large-v3-turbo",
		englishModel:          "canary-v2",
		parakeetModel:         "parakeet-v3",
		cohereModel:           "cohere-transcribe",
		health:                newHealthState(),
		langCapabilityRouting: true,
	}
	for _, m := range []string{"canary-v2", "large-v3-turbo", "parakeet-v3", "cohere-transcribe"} {
		g.health.set(m, true)
	}
	return g
}

// Every table row must point at a model that can actually serve the language —
// a row that names a backend outside its language set would silently fall
// through to the tiers forever (qualifies() rejects it).
func TestCloudSTTModels_EveryRowIsCapable(t *testing.T) {
	g := prodTableGateway()
	for lang, slot := range cloudSTTModels {
		m := g.slotModel(slot)
		if m == "" {
			t.Errorf("%s: slot %d has no production model", lang, slot)
			continue
		}
		if !g.backendServes(m, lang) {
			t.Errorf("%s: table routes to %s, which does not serve it", lang, m)
		}
	}
}

// With production config, every table language resolves to its table model.
func TestCloudSTTModels_ProdConfig_PicksTableModel(t *testing.T) {
	g := prodTableGateway()
	for lang, slot := range cloudSTTModels {
		want := g.slotModel(slot)
		if got := g.ModelForLanguage(lang); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (table)", lang, got, want)
		}
		if got := g.ModelForLanguage(strings.ToUpper(lang)); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (case-insensitive)", strings.ToUpper(lang), got, want)
		}
	}
}

// legacyModelForLanguage is today's routing before the table (2026-10-17,
// production config, everything healthy), frozen as the reference for
// "languages outside the table route exactly as before".
func legacyModelForLanguage(lang string) string {
	switch lang {
	case "", "en", "es", "de", "nl", "fr", "pt", "it":
		return "cohere-transcribe"
	case "pl":
		return "large-v3-turbo"
	}
	if euLanguages[lang] {
		return "canary-v2"
	}
	return "large-v3-turbo"
}

// Languages not in the table (and region-tagged codes, which never matched a
// tier) keep exactly the model they had before the table existed.
func TestCloudSTTModels_UnlistedLanguagesUnchanged(t *testing.T) {
	g := prodTableGateway()
	langs := []string{"cs", "sk", "bg", "hr", "da", "et", "fi", "hu", "lt", "lv", "mt",
		"ro", "sl", "sv", "uk", "el", "no", "sr", "th", "id", "he", "fa", "pt-br", "zh-hans", "auto"}
	for _, lang := range langs {
		if _, inTable := cloudSTTModels[lang]; inTable {
			t.Fatalf("%s is in the table; this test covers unlisted languages only", lang)
		}
		if got, want := g.ModelForLanguage(lang), legacyModelForLanguage(lang); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (unchanged)", lang, got, want)
		}
	}
}

// A table model that is down falls through to the tiers — the fallback chain
// is today's EU / non-EU chain, and the retry path re-runs this same function
// after the 5xx demotes the table model.
func TestCloudSTTModels_TableModelDown_FallsThroughToTiers(t *testing.T) {
	for lang, slot := range cloudSTTModels {
		g := prodTableGateway()
		down := g.slotModel(slot)
		g.health.set(down, false)
		got := g.ModelForLanguage(lang)
		var want string
		switch {
		case lang == "en":
			want = "canary-v2" // englishModel tier
		case euLanguages[lang] && down != "canary-v2":
			want = "canary-v2"
		case euLanguages[lang]:
			want = "large-v3-turbo"
		case down != "large-v3-turbo":
			want = "large-v3-turbo"
		default:
			want = "large-v3-turbo" // nothing else serves it: preferred model anyway
		}
		if got != want {
			t.Errorf("%s with %s down: got %q, want %q", lang, down, got, want)
		}
	}
}

// Slots the gateway does not configure (community build: no COHERE_MODEL,
// no PARAKEET_V3_MODEL) skip their table rows: those languages route through
// the tiers as they did before Cohere/the table existed.
func TestCloudSTTModels_UnconfiguredSlotsSkipRows(t *testing.T) {
	g := prodTableGateway()
	g.cohereModel = ""
	g.parakeetModel = ""
	for lang, slot := range cloudSTTModels {
		if slot != slotCohere && slot != slotParakeet {
			continue
		}
		want := "large-v3-turbo"
		if euLanguages[lang] {
			want = "canary-v2"
		}
		if got := g.ModelForLanguage(lang); got != want {
			t.Errorf("%s with slot unset: got %q, want %q", lang, got, want)
		}
	}
}

// Auto-detect agrees with explicit routing on which languages Cohere serves.
func TestModelForAutoDetect_CohereConfident_FollowsTable(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	for lang, slot := range cloudSTTModels {
		r := g.ModelForAutoDetect(AutoDetectContext{Profile: []langEntry{{Code: lang, Count: 9}}})
		isCohere := r.Tier == "cohere_confident"
		if isCohere != (slot == slotCohere) {
			t.Errorf("%s: tier %q, table slot %d", lang, r.Tier, slot)
		}
	}
}
