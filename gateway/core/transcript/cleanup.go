package transcript

import (
	"strings"
	"unicode"
)

var fillerSets = map[string]map[string]struct{}{
	"en": toSet("um", "uh", "er", "ah", "mm", "hmm", "umm", "uhh"),
	"es": toSet("eh", "em"),
	"de": toSet("äh", "ähm", "ehm"),
	"nl": toSet("eh", "uh", "hè"),
	"fr": toSet("euh", "hein"),
	"pt": toSet("é", "hum", "ahn"),
	"it": toSet("eh", "ehm", "mah"),
}

var repetitionExceptions = toSet("very", "no")

func toSet(words ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}

const sentenceEnders = ".!?"

type cleanupToken struct {
	word  string
	punct string
}

func CleanTranscript(text, language, provider string) (cleaned string, changed bool) {
	tokens := tokenizeCleanup(text)
	if len(tokens) == 0 {
		return text, false
	}

	if provider != "whisper" {
		tokens = removeFillers(tokens, language)
	}
	tokens = dedupTokens(tokens)

	cleaned = reconstructCleanup(tokens)
	return cleaned, cleaned != text
}

func tokenizeCleanup(text string) []cleanupToken {
	fields := strings.Fields(text)
	tokens := make([]cleanupToken, 0, len(fields))
	for _, f := range fields {
		word, punct := splitTrailingPunct(f)
		tokens = append(tokens, cleanupToken{word: word, punct: punct})
	}
	return tokens
}

func splitTrailingPunct(field string) (word, punct string) {
	runes := []rune(field)
	end := len(runes)
	for end > 0 && isTrailingPunct(runes[end-1]) {
		end--
	}
	return string(runes[:end]), string(runes[end:])
}

func isTrailingPunct(r rune) bool {
	switch r {
	case ',', '.', '!', '?', ';', ':':
		return true
	default:
		return false
	}
}

func removeFillers(tokens []cleanupToken, language string) []cleanupToken {
	fillers, ok := fillerSets[language]
	if !ok || len(fillers) == 0 {
		return tokens
	}

	out := make([]cleanupToken, 0, len(tokens))
	atSentenceStart := true
	for _, tok := range tokens {
		_, isFiller := fillers[strings.ToLower(tok.word)]
		if isFiller {
			if atSentenceStart && startsUpper(tok.word) {
				out = append(out, cleanupToken{word: "", punct: ""})
			}
			continue
		}
		out = append(out, tok)
		atSentenceStart = endsSentence(tok.punct)
	}

	return applyPendingCapitalization(out)
}

func startsUpper(word string) bool {
	for _, r := range word {
		return unicode.IsUpper(r)
	}
	return false
}

func endsSentence(punct string) bool {
	if punct == "" {
		return false
	}
	last := punct[len(punct)-1]
	return strings.IndexByte(sentenceEnders, last) >= 0
}

func applyPendingCapitalization(tokens []cleanupToken) []cleanupToken {
	out := make([]cleanupToken, 0, len(tokens))
	pending := false
	for _, tok := range tokens {
		if tok.word == "" && tok.punct == "" {
			pending = true
			continue
		}
		if pending {
			tok.word = uppercaseFirst(tok.word)
			pending = false
		}
		out = append(out, tok)
	}
	return out
}

func uppercaseFirst(word string) string {
	if word == "" {
		return word
	}
	runes := []rune(word)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func dedupTokens(tokens []cleanupToken) []cleanupToken {
	out := make([]cleanupToken, 0, len(tokens))
	for _, tok := range tokens {
		if n := len(out); n > 0 {
			prev := out[n-1]
			lower := strings.ToLower(tok.word)
			if lower != "" && lower == strings.ToLower(prev.word) {
				if _, excepted := repetitionExceptions[lower]; !excepted {
					continue
				}
			}
		}
		out = append(out, tok)
	}
	return out
}

func reconstructCleanup(tokens []cleanupToken) string {
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.word == "" && tok.punct == "" {
			continue
		}
		parts = append(parts, tok.word+tok.punct)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
