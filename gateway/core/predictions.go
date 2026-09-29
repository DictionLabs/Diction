package core

import (
	"strings"
	"unicode"
)

// MaxPredictions is how many next-word predictions /v1/text/predict returns: one per chip.
const MaxPredictions = 3

// SanitizePredictions turns the model's ranked next-word candidates into what a chip can insert
// as one word: each trimmed of surrounding punctuation (an apostrophe or hyphen inside the word
// stays), entries with a space or nothing left dropped, duplicates dropped case-insensitively
// (the first, higher-ranked form wins), and the first MaxPredictions kept in the model's order.
// The prompt asks for more candidates than that, so a dropped one lets the next in. Never nil,
// so the response encodes as []. Shared by the gateway handler and the prompt-eval Predict suite.
func SanitizePredictions(in []string) []string {
	out := make([]string, 0, MaxPredictions)
	seen := make(map[string]bool, len(in))
	for _, p := range in {
		w := strings.TrimFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if w == "" || strings.IndexFunc(w, unicode.IsSpace) >= 0 {
			continue
		}
		key := strings.ToLower(w)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, w)
		if len(out) == MaxPredictions {
			break
		}
	}
	return out
}
