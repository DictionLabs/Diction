package core

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
)

// concreteLanguagePattern is the shape gate for a normalized language code: two or three
// lowercase letters, no region subtag. `IsConcreteLanguage` normalizes (lowercase, strip
// "-region") before matching, so "CS", "cs-CZ" and "cs" all pass identically.
var concreteLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

// normalizeLanguageCode lowercases and strips a region subtag: "pt-BR" -> "pt". Mirrors
// WhisperDecoding.normalizedLanguageCode on the iOS side.
func normalizeLanguageCode(lang string) string {
	lowered := strings.ToLower(strings.TrimSpace(lang))
	if idx := strings.IndexByte(lowered, '-'); idx >= 0 {
		lowered = lowered[:idx]
	}
	return lowered
}

// IsConcreteLanguage reports whether lang is a real, usable language code — not empty, not
// the auto-detect sentinel, and shaped like a bare ISO 639 code once normalized. This value
// is about to be spliced into an LLM prompt tag (`<language>…</language>` / `(Language: …)`),
// and the batch `?language=` query has no shape gate elsewhere in core (unlike the realtime
// socket's `realtimeLanguagePattern`), so this is the only thing standing between arbitrary
// client text and the prompt.
func IsConcreteLanguage(lang string) bool {
	if lang == "" || IsAutoDetect(lang) {
		return false
	}
	return concreteLanguagePattern.MatchString(normalizeLanguageCode(lang))
}

// WithContextLanguage returns contextJSON with its "language" key set to the normalized form
// of language, overriding whatever the client sent — the gateway-resolved value (STT-detected
// under auto-detect, or the routing query) is more authoritative than what the client blob
// carries, and this repairs older clients that injected a stale picker value under
// auto-detect. When language is not concrete (empty or "auto"), contextJSON is returned
// unchanged: the caller is expected to let the model infer, never to inject "auto" as a
// literal hint. On a JSON parse error, contextJSON is returned unchanged and the error is
// logged once — this never blocks the request over a malformed blob.
func WithContextLanguage(contextJSON, language string) string {
	if !IsConcreteLanguage(language) {
		return contextJSON
	}
	var obj map[string]interface{}
	if contextJSON == "" {
		obj = map[string]interface{}{}
	} else if err := json.Unmarshal([]byte(contextJSON), &obj); err != nil {
		log.Printf("WithContextLanguage: parse error, passing context through unchanged: %v", err)
		return contextJSON
	}
	obj["language"] = normalizeLanguageCode(language)
	out, err := json.Marshal(obj)
	if err != nil {
		log.Printf("WithContextLanguage: marshal error, passing context through unchanged: %v", err)
		return contextJSON
	}
	return string(out)
}
