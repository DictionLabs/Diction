package core

import (
	"strings"
	"unicode"
)

// fillerSets is the per-language filler-word vocabulary. English mirrors the
// shipped cloud LLM prompt exactly (gateway/internal/prompts/prompts.go)
// so cross-tier output stays consistent; the rest are a first, unverified
// pass seeded from the cohere-production language set. A language absent
// from this map (including the empty string, e.g. unresolved auto-detect)
// gets no filler removal — only the dedup pass runs, which is always safe.
var fillerSets = map[string]map[string]struct{}{
	"en": toSet("um", "uh", "er", "ah", "mm", "hmm", "umm", "uhh"),
	"es": toSet("eh", "em"),
	"de": toSet("äh", "ähm", "ehm"),
	"nl": toSet("eh", "uh", "hè"),
	"fr": toSet("euh", "hein"),
	"pt": toSet("é", "hum", "ahn"),
	"it": toSet("eh", "ehm", "mah"),
}

// repetitionExceptions are words that legitimately double up in natural
// speech ("no no I don't want that", "very very tired") and must never be
// collapsed by the dedup pass.
var repetitionExceptions = toSet("very", "no")

func toSet(words ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}

// sentenceEnders mark the end of a sentence for the capitalization-transfer
// rule in removeFillers — text after one of these keeps its own capitalization.
const sentenceEnders = ".!?"

// cleanupToken is a word split from its trailing punctuation run, so bare
// words can be compared case-insensitively while punctuation reconstructs
// verbatim.
type cleanupToken struct {
	word  string // bare word, original case
	punct string // trailing punctuation run (may be empty)
}

// CleanTranscript applies the deterministic filler + repetition removal
// layer. provider == "whisper" gets light-touch treatment (dedup only, since
// Whisper output is already near display-form); every other provider
// ("parakeet", "canary", or anything else) gets full treatment (filler
// removal + dedup). language should be the actually-detected/resolved
// language code when known — an unseeded or empty language still gets the
// (always-safe) dedup pass. Never returns an empty string for non-empty
// input, and never modifies text it isn't confident about — the failure
// mode is identity.
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

// tokenizeCleanup splits text on whitespace and separates each token's
// trailing punctuation run from its bare word.
func tokenizeCleanup(text string) []cleanupToken {
	fields := strings.Fields(text)
	tokens := make([]cleanupToken, 0, len(fields))
	for _, f := range fields {
		word, punct := splitTrailingPunct(f)
		tokens = append(tokens, cleanupToken{word: word, punct: punct})
	}
	return tokens
}

// splitTrailingPunct splits a field into its bare word and trailing
// punctuation run (,.!?;:).
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

// removeFillers drops tokens whose bare word (lowercased) is in language's
// seeded filler set. When the dropped token was the leading word of a
// sentence (start of transcript, or immediately after a sentence-ending
// token) and carried no meaning of its own, its capitalization is
// transferred to the new leading word of that sentence; the dropped
// token's own punctuation is discarded.
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
				// Transfer the dropped leading filler's capitalization to
				// the next surviving token, once found.
				out = append(out, cleanupToken{word: "", punct: ""})
			}
			// A filler never itself carries the "sentence just ended" state
			// forward beyond what it inherited.
			continue
		}
		out = append(out, tok)
		atSentenceStart = endsSentence(tok.punct)
	}

	return applyPendingCapitalization(out)
}

// startsUpper reports whether word's first rune is uppercase.
func startsUpper(word string) bool {
	for _, r := range word {
		return unicode.IsUpper(r)
	}
	return false
}

// endsSentence reports whether punct ends a sentence.
func endsSentence(punct string) bool {
	if punct == "" {
		return false
	}
	last := punct[len(punct)-1]
	return strings.IndexByte(sentenceEnders, last) >= 0
}

// applyPendingCapitalization resolves the placeholder empty tokens left by
// removeFillers (marking "a sentence-initial filler was dropped here") by
// uppercasing the first letter of the next real token and dropping the
// placeholder.
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

// dedupTokens collapses an immediately-adjacent pair of tokens whose bare
// words match case-insensitively, unless the bare word is a repetition
// exception. The first occurrence's original text (case + punctuation) is
// kept as the canonical form.
func dedupTokens(tokens []cleanupToken) []cleanupToken {
	out := make([]cleanupToken, 0, len(tokens))
	for _, tok := range tokens {
		if n := len(out); n > 0 {
			prev := out[n-1]
			lower := strings.ToLower(tok.word)
			if lower != "" && lower == strings.ToLower(prev.word) {
				if _, excepted := repetitionExceptions[lower]; !excepted {
					// Drop the second occurrence entirely; the first
					// occurrence's case + punctuation is the canonical form.
					continue
				}
			}
		}
		out = append(out, tok)
	}
	return out
}

// reconstructCleanup joins tokens with single spaces and reattaches each
// token's trailing punctuation, then trims leading/trailing whitespace.
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
