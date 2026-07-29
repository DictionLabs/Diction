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

func TestModelForLanguage_HealthFallback_TurboDown(t *testing.T) {
	// large-v3-turbo is down, non-EU language → fall back to canary (better than nothing).
	g := &Gateway{
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		health:        newHealthState(),
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("ja"); got != "canary-v2" {
		t.Errorf("got %q, want canary-v2 (health fallback)", got)
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
	// "pl" deliberately excluded here — see nonEUModelOverrides / the Polish
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

func TestModelForLanguage_Polish_HealthyFallback(t *testing.T) {
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage("pl"); got != "large-v3-turbo" {
		t.Errorf("pl: got %q, want large-v3-turbo", got)
	}
}

func TestModelForLanguage_Polish_FallbackDown_UsesDefault(t *testing.T) {
	g := testGatewayWithCohere()
	g.health.set("large-v3-turbo", false)
	if got := g.ModelForLanguage("pl"); got != "canary-v2" {
		t.Errorf("pl (turbo down): got %q, want canary-v2", got)
	}
}

func TestModelForLanguage_EUOnlyAndGreek_UnchangedOnCanary(t *testing.T) {
	// Full regression surface: every EU-set code Cohere doesn't support, plus
	// Greek (technically Cohere-supported but excluded — no measured data),
	// must still resolve to canary-1b-v2 exactly as before this change.
	g := testGatewayWithCohere()
	untouched := []string{
		"bg", "hr", "cs", "da", "et", "fi", "hu", "lt", "lv",
		"mt", "ro", "sk", "sl", "sv", "ru", "uk", "el",
	}
	for _, lang := range untouched {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (unaffected by Cohere rollout)", lang, got)
		}
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
