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
		{"EN", true},
		{" en ", true},
	}
	for _, tt := range tests {
		if got := IsEULanguage(tt.lang); got != tt.want {
			t.Errorf("IsEULanguage(%q) = %v, want %v", tt.lang, got, tt.want)
		}
	}
}

func TestModelForLanguage_NoFallback(t *testing.T) {
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

func TestModelForLanguage_HealthFallback_TurboDown_PolishStillFallsBack(t *testing.T) {
	g := languageCapableTestGateway()
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("pl"); got != "canary-v2" {
		t.Errorf("ModelForLanguage(pl) = %q, want canary-v2 (it does serve Polish)", got)
	}
}

func TestModelForLanguage_HealthFallback_TurboDown_AutoNeverGoesToCanary(t *testing.T) {
	g := languageCapableTestGateway()
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", false)

	if got := g.ModelForLanguage("auto"); got != "large-v3-turbo" {
		t.Errorf("ModelForLanguage(auto) = %q, want large-v3-turbo (canary cannot do its own language ID)", got)
	}
}

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
	if got := len(euLanguages); got != 25 {
		t.Errorf("euLanguages has %d entries, want 25", got)
	}
}

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
	g := testGatewayThreeTier()
	if got := g.ModelForLanguage(""); got != "canary-qwen" {
		t.Errorf("got %q, want canary-qwen", got)
	}
}

func TestModelForLanguage_OtherEU_UsesDefaultModel(t *testing.T) {
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
		{"en,fr", false},
		{"automatic", false},
	}
	for _, tt := range tests {
		if got := IsAutoDetect(tt.in); got != tt.want {
			t.Errorf("IsAutoDetect(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

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
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_safe" || result.UpstreamLanguage != "" {
		t.Errorf("got model=%q tier=%q lang=%q, want large-v3-turbo/whisper_safe/\"\"",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_WhisperHistory_NonEU(t *testing.T) {
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "zh", Count: 3}, {Code: "cs", Count: 2}},
	})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_history" {
		t.Errorf("got model=%q tier=%q, want large-v3-turbo/whisper_history", result.Model, result.Tier)
	}
}

func TestModelForAutoDetect_ParakeetHistory_AllEU(t *testing.T) {
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "cs", Count: 3}, {Code: "sk", Count: 2}},
	})
	if result.Model != "parakeet-v3" || result.Tier != "parakeet_history" || result.UpstreamLanguage != "" {
		t.Errorf("got model=%q tier=%q lang=%q, want parakeet-v3/parakeet_history/\"\"",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_CanaryConfident_DominantEU(t *testing.T) {
	g := testAutoDetectGateway()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" || result.UpstreamLanguage != "cs" {
		t.Errorf("got model=%q tier=%q lang=%q, want canary-v2/canary_confident/cs",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_CanaryDown_FallsToParakeet(t *testing.T) {
	g := testAutoDetectGateway()
	g.health.set("canary-v2", false)
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "parakeet-v3" || result.Tier != "parakeet_history" {
		t.Errorf("canary down: got model=%q tier=%q, want parakeet-v3/parakeet_history",
			result.Model, result.Tier)
	}
}

func TestModelForAutoDetect_ParakeetDown_FallsToWhisper(t *testing.T) {
	g := testAutoDetectGateway()
	g.health.set("parakeet-v3", false)
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "cs", Count: 3}, {Code: "sk", Count: 2}},
	})
	if result.Model != "large-v3-turbo" || result.Tier != "whisper_history" {
		t.Errorf("parakeet down: got model=%q tier=%q, want large-v3-turbo/whisper_history",
			result.Model, result.Tier)
	}
}

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
	g := testGatewayWithCohere()
	if got := g.ModelForLanguage(""); got != "cohere-transcribe" {
		t.Errorf("got %q, want cohere-transcribe", got)
	}
}

func TestModelForLanguage_CohereUnhealthy_FallsToExistingTiers(t *testing.T) {
	g := testGatewayWithCohere()
	g.health.set("cohere-transcribe", false)
	if got := g.ModelForLanguage("en"); got != "canary-v2" {
		t.Errorf("en: got %q, want canary-v2 (cohere down)", got)
	}
	for _, lang := range []string{"es", "de", "nl", "fr", "pt", "it"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (cohere down)", lang, got)
		}
	}
}

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
	g := testGatewayWithCohere()
	untouched := []string{
		"bg", "hr", "cs", "da", "et", "fi", "hu", "lt", "lv",
		"mt", "ro", "sk", "sl", "sv", "uk",
	}
	for _, lang := range untouched {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (unaffected by Cohere rollout)", lang, got)
		}
	}
}

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
	g := testGatewayWithCohere()
	g.cohereModel = ""
	for _, lang := range []string{"en", "es", "de", "nl", "fr", "pt", "it"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("lang=%q: got %q, want canary-v2 (cohere kill switch)", lang, got)
		}
	}
}

func testAutoDetectGatewayWithCohere() *Gateway {
	g := testAutoDetectGateway()
	g.cohereModel = "cohere-transcribe"
	g.health.set("cohere-transcribe", true)
	return g
}

func TestModelForAutoDetect_CohereConfident_DominantCohereLang(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "es", Count: 5}},
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
		Profile: []LangEntry{{Code: "es", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" || result.UpstreamLanguage != "es" {
		t.Errorf("cohere down: got model=%q tier=%q lang=%q, want canary-v2/canary_confident/es",
			result.Model, result.Tier, result.UpstreamLanguage)
	}
}

func TestModelForAutoDetect_DominantNonCohereEU_StillCanaryConfident(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	result := g.ModelForAutoDetect(AutoDetectContext{
		Profile: []LangEntry{{Code: "cs", Count: 5}},
	})
	if result.Model != "canary-v2" || result.Tier != "canary_confident" {
		t.Errorf("got model=%q tier=%q, want canary-v2/canary_confident", result.Model, result.Tier)
	}
}

func prodTableGateway() *Gateway {
	g := &Gateway{
		backends:              DefaultBackends(),
		defaultModel:          "canary-v2",
		fallbackModel:         "large-v3-turbo",
		englishModel:          "canary-v2",
		parakeetModel:         "parakeet-v3",
		cohereModel:           "cohere-transcribe",
		qwenModel:             "qwen3-asr",
		health:                newHealthState(),
		langCapabilityRouting: true,
	}
	for _, m := range []string{"canary-v2", "large-v3-turbo", "parakeet-v3", "cohere-transcribe", "qwen3-asr"} {
		g.health.set(m, true)
	}
	return g
}

func TestCloudSTTModels_EveryRowIsCapable(t *testing.T) {
	g := prodTableGateway()
	for lang, slots := range cloudSTTModels {
		if len(slots) == 0 {
			t.Errorf("%s: empty row", lang)
		}
		for _, slot := range slots {
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
}

func TestCloudSTTModels_ProdConfig_PicksTableModel(t *testing.T) {
	g := prodTableGateway()
	for lang, slots := range cloudSTTModels {
		want := g.slotModel(slots[0])
		if got := g.ModelForLanguage(lang); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (table)", lang, got, want)
		}
		if got := g.ModelForLanguage(strings.ToUpper(lang)); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (case-insensitive)", strings.ToUpper(lang), got, want)
		}
	}
}

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

func TestCloudSTTModels_UnlistedLanguagesUnchanged(t *testing.T) {
	g := prodTableGateway()
	langs := []string{"cs", "sk", "bg", "hr", "et", "lv", "mt",
		"ro", "sl", "sv", "no", "sr", "th", "id", "he", "fa", "pt-br", "zh-hans", "auto"}
	for _, lang := range langs {
		if _, inTable := cloudSTTModels[lang]; inTable {
			t.Fatalf("%s is in the table; this test covers unlisted languages only", lang)
		}
		if got, want := g.ModelForLanguage(lang), legacyModelForLanguage(lang); got != want {
			t.Errorf("ModelForLanguage(%q) = %q, want %q (unchanged)", lang, got, want)
		}
	}
}

func TestCloudSTTModels_TableModelDown_FallsThroughToTiers(t *testing.T) {
	for lang, slots := range cloudSTTModels {
		g := prodTableGateway()
		down := g.slotModel(slots[0])
		g.health.set(down, false)
		got := g.ModelForLanguage(lang)
		var want string
		switch {
		case len(slots) > 1:
			want = g.slotModel(slots[1])
		case lang == "en":
			want = "canary-v2"
		case euLanguages[lang] && down != "canary-v2":
			want = "canary-v2"
		case euLanguages[lang]:
			want = "large-v3-turbo"
		case down != "large-v3-turbo":
			want = "large-v3-turbo"
		default:
			want = "large-v3-turbo"
		}
		if got != want {
			t.Errorf("%s with %s down: got %q, want %q", lang, down, got, want)
		}
	}
}

func TestCloudSTTModels_UnconfiguredSlotsSkipRows(t *testing.T) {
	g := prodTableGateway()
	g.cohereModel = ""
	g.parakeetModel = ""
	g.qwenModel = ""
	for lang, slots := range cloudSTTModels {
		want := ""
		for _, slot := range slots {
			if m := g.slotModel(slot); m != "" {
				want = m
				break
			}
		}
		if want == "" {
			want = "large-v3-turbo"
			if euLanguages[lang] {
				want = "canary-v2"
			}
		}
		if got := g.ModelForLanguage(lang); got != want {
			t.Errorf("%s with slot unset: got %q, want %q", lang, got, want)
		}
	}
}

func TestModelForAutoDetect_CohereConfident_FollowsTable(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	for lang := range cloudSTTModels {
		r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: lang, Count: 9}}})
		isCohere := r.Tier == "cohere_confident"
		if isCohere != (g.tableModel(lang) == "cohere-transcribe") {
			t.Errorf("%s: tier %q, table model %q", lang, r.Tier, g.tableModel(lang))
		}
	}
}

func TestCloudSTTModels_QwenRows(t *testing.T) {
	cases := []struct {
		name       string
		lang       string
		qwenSlot   string
		qwenHealth bool
		want       string
	}{
		{"zh healthy", "zh", "qwen3-asr", true, "qwen3-asr"},
		{"zh qwen down -> cohere (today's model)", "zh", "qwen3-asr", false, "cohere-transcribe"},
		{"zh slot unset -> cohere", "zh", "", true, "cohere-transcribe"},
		{"yue healthy", "yue", "qwen3-asr", true, "qwen3-asr"},
		{"yue qwen down -> turbo (today's model)", "yue", "qwen3-asr", false, "large-v3-turbo"},
		{"yue slot unset -> turbo", "yue", "", true, "large-v3-turbo"},
		{"ZH upper", "ZH", "qwen3-asr", true, "qwen3-asr"},
	}
	for _, tc := range cases {
		g := prodTableGateway()
		g.qwenModel = tc.qwenSlot
		g.health.set("qwen3-asr", tc.qwenHealth)
		if got := g.ModelForLanguage(tc.lang); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	g := prodTableGateway()
	for _, lang := range []string{"en", "ja", "ko", "de", "ru", "pl", "vi", "hi", "cs", "auto", ""} {
		if got := g.ModelForLanguage(lang); got == "qwen3-asr" {
			t.Errorf("%q routed to qwen3-asr", lang)
		}
	}
}

func TestModelForAutoDetect_QwenConfident(t *testing.T) {
	g := prodTableGateway()
	for _, lang := range []string{"zh", "yue"} {
		r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: lang, Count: 9}}})
		if r.Model != "qwen3-asr" || r.Tier != "qwen_confident" || r.UpstreamLanguage != lang {
			t.Errorf("%s: got %q/%q/%q, want qwen3-asr/qwen_confident/%s", lang, r.Model, r.Tier, r.UpstreamLanguage, lang)
		}
	}
	g.health.set("qwen3-asr", false)
	if r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "zh", Count: 9}}}); r.Tier != "cohere_confident" || r.Model != "cohere-transcribe" {
		t.Errorf("zh with qwen down: got %q/%q, want cohere-transcribe/cohere_confident", r.Model, r.Tier)
	}
	if r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "yue", Count: 9}}}); r.Model != "large-v3-turbo" {
		t.Errorf("yue with qwen down: got %q/%q, want large-v3-turbo", r.Model, r.Tier)
	}
	g = prodTableGateway()
	g.qwenModel, g.cohereModel = "", ""
	if r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "zh", Count: 9}}}); r.Tier == "qwen_confident" || r.Tier == "cohere_confident" {
		t.Errorf("zh with no qwen/cohere slot: tier %q", r.Tier)
	}
}

func TestCloudSTTModels_ParakeetRows(t *testing.T) {
	cases := []struct {
		lang, today string
	}{
		{"da", "canary-v2"}, {"fi", "canary-v2"}, {"hu", "canary-v2"}, {"uk", "canary-v2"},
		{"ru", "large-v3-turbo"},
	}
	for _, tc := range cases {
		g := prodTableGateway()
		if got := g.ModelForLanguage(tc.lang); got != "parakeet-v3" {
			t.Errorf("%s: got %q, want parakeet-v3", tc.lang, got)
		}
		g.health.set("parakeet-v3", false)
		if got := g.ModelForLanguage(tc.lang); got != tc.today {
			t.Errorf("%s with parakeet down: got %q, want %q (today's model)", tc.lang, got, tc.today)
		}
		g = prodTableGateway()
		g.parakeetModel = ""
		if got := g.ModelForLanguage(tc.lang); got != tc.today {
			t.Errorf("%s with parakeet slot unset: got %q, want %q", tc.lang, got, tc.today)
		}
	}
	g := prodTableGateway()
	for _, lang := range []string{"en", "de", "es", "fr", "it", "nl", "pt", "pl", "cs", "sk", "bg",
		"hr", "et", "ro", "sv", "el", "lv", "mt", "sl", ""} {
		if got := g.ModelForLanguage(lang); got == "parakeet-v3" {
			t.Errorf("%q routed to parakeet-v3", lang)
		}
	}
}

func TestModelForAutoDetect_ParakeetConfident(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	for _, lang := range []string{"da", "fi", "hu", "uk", "ru", "lt"} {
		r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: lang, Count: 9}}})
		if r.Model != "parakeet-v3" || r.Tier != "parakeet_confident" || r.UpstreamLanguage != "" {
			t.Errorf("%s: got %q/%q/%q, want parakeet-v3/parakeet_confident/\"\"", lang, r.Model, r.Tier, r.UpstreamLanguage)
		}
	}
	g.health.set("parakeet-v3", false)
	if r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "uk", Count: 9}}}); r.Model != "canary-v2" || r.Tier != "canary_confident" || r.UpstreamLanguage != "uk" {
		t.Errorf("uk with parakeet down: got %q/%q/%q, want canary-v2/canary_confident/uk", r.Model, r.Tier, r.UpstreamLanguage)
	}
	g.health.set("parakeet-v3", true)
	if r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "cs", Count: 9}}}); r.Tier != "canary_confident" {
		t.Errorf("cs: tier %q, want canary_confident", r.Tier)
	}
}

func TestCloudSTTModels_GreekAndLithuanianRows(t *testing.T) {
	cases := []struct {
		lang, first, second string
	}{
		{"el", "cohere-transcribe", "large-v3-turbo"},
		{"lt", "parakeet-v3", "canary-v2"},
	}
	for _, tc := range cases {
		g := prodTableGateway()
		if got := g.ModelForLanguage(tc.lang); got != tc.first {
			t.Errorf("%s: got %q, want %q", tc.lang, got, tc.first)
		}
		g.health.set(tc.first, false)
		if got := g.ModelForLanguage(tc.lang); got != tc.second {
			t.Errorf("%s with %s down: got %q, want %q", tc.lang, tc.first, got, tc.second)
		}
	}
	g := prodTableGateway()
	g.cohereModel, g.parakeetModel, g.fallbackModel = "", "", ""
	for _, lang := range []string{"el", "lt"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("%s with slots unset: got %q, want canary-v2", lang, got)
		}
	}
	g = prodTableGateway()
	for _, lang := range []string{"lv", "mt", "sl"} {
		if got := g.ModelForLanguage(lang); got != "canary-v2" {
			t.Errorf("%s: got %q, want canary-v2", lang, got)
		}
	}
}

func TestModelForAutoDetect_GreekCohereConfident(t *testing.T) {
	g := testAutoDetectGatewayWithCohere()
	r := g.ModelForAutoDetect(AutoDetectContext{Profile: []LangEntry{{Code: "el", Count: 9}}})
	if r.Model != "cohere-transcribe" || r.Tier != "cohere_confident" || r.UpstreamLanguage != "el" {
		t.Errorf("el: got %q/%q/%q, want cohere-transcribe/cohere_confident/el", r.Model, r.Tier, r.UpstreamLanguage)
	}
}
