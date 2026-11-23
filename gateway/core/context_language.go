package core

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
)

var concreteLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

func normalizeLanguageCode(lang string) string {
	lowered := strings.ToLower(strings.TrimSpace(lang))
	if idx := strings.IndexByte(lowered, '-'); idx >= 0 {
		lowered = lowered[:idx]
	}
	return lowered
}

func IsConcreteLanguage(lang string) bool {
	if lang == "" || IsAutoDetect(lang) {
		return false
	}
	return concreteLanguagePattern.MatchString(normalizeLanguageCode(lang))
}

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
