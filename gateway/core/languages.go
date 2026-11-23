package core

import (
	"strings"
)

const AutoDetectSentinel = "auto"

func IsAutoDetect(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), AutoDetectSentinel)
}

var euLanguages = map[string]bool{
	"en": true, "bg": true, "hr": true, "cs": true, "da": true,
	"nl": true, "et": true, "fi": true, "fr": true, "de": true,
	"el": true, "hu": true, "it": true, "lv": true, "lt": true,
	"mt": true, "pl": true, "pt": true, "ro": true, "sk": true,
	"sl": true, "es": true, "sv": true, "ru": true, "uk": true,
}

func IsEULanguage(lang string) bool {
	return euLanguages[strings.TrimSpace(strings.ToLower(lang))]
}

var cohereSupported = map[string]bool{
	"en": true, "de": true, "fr": true, "it": true, "es": true, "pt": true, "el": true,
	"nl": true, "pl": true, "ar": true, "vi": true, "zh": true, "ja": true, "ko": true,
}

type sttSlot int

const (
	slotCohere sttSlot = iota + 1
	slotParakeet
	slotDefault
	slotFallback
	slotQwen
)

func chain(slots ...sttSlot) []sttSlot { return slots }

var cloudSTTModels = map[string][]sttSlot{
	"en": chain(slotCohere), "es": chain(slotCohere), "pt": chain(slotCohere), "fr": chain(slotCohere),
	"de": chain(slotCohere), "it": chain(slotCohere), "nl": chain(slotCohere),

	"pl": chain(slotCohere),
	"ar": chain(slotCohere),
	"vi": chain(slotCohere),
	"zh": chain(slotQwen, slotCohere),

	"ja": chain(slotFallback), "ko": chain(slotFallback), "hi": chain(slotFallback), "tr": chain(slotFallback),

	langYue: chain(slotQwen, slotFallback),

	"da": chain(slotParakeet, slotDefault), "fi": chain(slotParakeet, slotDefault),
	"hu": chain(slotParakeet, slotDefault), "uk": chain(slotParakeet, slotDefault),
	"ru": chain(slotParakeet, slotFallback),

	"lt": chain(slotParakeet, slotDefault),
	"el": chain(slotCohere, slotFallback),
}

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
	case slotQwen:
		return g.qwenModel
	}
	return ""
}

func (g *Gateway) tableModel(lang string) string {
	for _, slot := range cloudSTTModels[lang] {
		if m := g.slotModel(slot); m != "" && g.qualifies(m, lang) {
			return m
		}
	}
	return ""
}

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

func (g *Gateway) qualifies(model, lang string) bool {
	if !g.healthy(model) {
		return false
	}
	if !g.langCapabilityRouting {
		return true
	}
	return g.backendServes(model, lang)
}

func (g *Gateway) ModelForLanguage(lang string) string {
	if g.fallbackModel == "" {
		return g.defaultModel
	}

	lang = strings.TrimSpace(strings.ToLower(lang))

	effectiveLang := lang
	if effectiveLang == "" {
		effectiveLang = "en"
	}

	if m := g.tableModel(effectiveLang); m != "" {
		return m
	}

	if g.englishModel != "" && (lang == "en" || lang == "") {
		if g.healthy(g.englishModel) {
			return g.englishModel
		}
	}

	if lang == "" || euLanguages[lang] {
		if g.qualifies(g.defaultModel, effectiveLang) {
			return g.defaultModel
		}
		if g.qualifies(g.fallbackModel, effectiveLang) {
			return g.fallbackModel
		}
		return g.defaultModel
	}

	if g.qualifies(g.fallbackModel, effectiveLang) {
		return g.fallbackModel
	}
	if g.qualifies(g.defaultModel, effectiveLang) {
		return g.defaultModel
	}
	return g.fallbackModel
}

type AutoDetectContext struct {
	DeviceHash string
	Profile    []LangEntry
}

type AutoDetectResult struct {
	Model            string
	UpstreamLanguage string
	Tier             string
}

func (g *Gateway) ModelForAutoDetect(ctx AutoDetectContext) AutoDetectResult {
	if g.fallbackModel == "" {
		return AutoDetectResult{}
	}

	minCount := EnvIntOrDefault("DETECT_MIN_COUNT", 5)
	minPct := EnvFloatOrDefault("DETECT_MIN_PCT", 0.90)

	dom := dominantLang(ctx.Profile, minCount, minPct)
	if r, ok := g.confidentTier(dom); ok {
		return r
	}

	allEU, hasHistory := euProfile(ctx.Profile)
	if hasHistory {
		if allEU && g.parakeetModel != "" && g.healthy(g.parakeetModel) {
			return AutoDetectResult{Model: g.parakeetModel, Tier: "parakeet_history"}
		}
		return AutoDetectResult{Model: g.fallbackModel, Tier: "whisper_history"}
	}

	return AutoDetectResult{Model: g.fallbackModel, Tier: "whisper_safe"}
}

func (g *Gateway) confidentTier(dom string) (AutoDetectResult, bool) {
	if dom == "" {
		return AutoDetectResult{}, false
	}
	if r, ok := g.tableTier(dom); ok {
		return r, true
	}
	if g.defaultModel != "" && g.healthy(g.defaultModel) && IsEULanguage(dom) {
		return AutoDetectResult{Model: g.defaultModel, UpstreamLanguage: dom, Tier: "canary_confident"}, true
	}
	return AutoDetectResult{}, false
}

func (g *Gateway) tableTier(dom string) (AutoDetectResult, bool) {
	tm := g.tableModel(strings.TrimSpace(strings.ToLower(dom)))
	switch {
	case tm == "":
	case g.qwenModel != "" && tm == g.qwenModel:
		return AutoDetectResult{Model: g.qwenModel, UpstreamLanguage: dom, Tier: "qwen_confident"}, true
	case g.cohereModel != "" && tm == g.cohereModel:
		return AutoDetectResult{Model: g.cohereModel, UpstreamLanguage: dom, Tier: "cohere_confident"}, true
	case g.parakeetModel != "" && tm == g.parakeetModel:
		return AutoDetectResult{Model: g.parakeetModel, Tier: "parakeet_confident"}, true
	}
	return AutoDetectResult{}, false
}

func euProfile(entries []LangEntry) (allEU bool, hasEntries bool) {
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

func dominantLang(entries []LangEntry, minCount int, minPct float64) string {
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
