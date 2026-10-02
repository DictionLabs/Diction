package core

import (
	"sort"
	"strings"
	"unicode"
)

// SanitizeSessionContext rewrites earlier dictations before they reach the cleanup prompt as
// <session_context>, so they cannot contradict the user's custom words.
//
// The app sends the last few final transcripts from the same app as session context, and the
// prompt tells the LLM to take proper-noun spellings from it. When those earlier dictations
// carried the same mishearing the user is now fixing ("Andre McCullough" for Ondrej Machala),
// the session outvoted the custom-word hint: gpt-oss-120b restored the names ~2/10 with the
// poisoned session and 10/10 without it (2026-09-29).
//
// Every span the vocabulary matcher reads as a mishearing of a custom word — in the current
// transcript, and in each session line on its own, since an earlier line may hold a different
// mishearing ("McCullough" while the utterance says "Makala") — is replaced in the session lines
// by the custom word. Rewriting rather than dropping the line keeps what the session is for (the
// topic and every other term in it), and turns the line from evidence against the fix into
// evidence for it. Spans the matcher deliberately leaves unpointed (the canonical spelling
// itself, or ordinary words like "on the door") are never touched. A line where one span is
// claimed by two different custom words is dropped instead: picking one would be a guess, and
// leaving it would keep the mishearing.
//
// The input slice is not modified. forms is the same [canonical, variants...] list passed to
// MatchVocabulary.
func SanitizeSessionContext(transcript string, session []string, forms [][]string) []string {
	if len(session) == 0 || len(forms) == 0 {
		return session
	}
	global := heardSpans(transcript, forms)
	out := make([]string, 0, len(session))
	for _, line := range session {
		spans := heardSpans(line, forms)
		for heard, words := range global {
			for w := range words {
				addHeard(spans, heard, w)
			}
		}
		rewritten, ok := rewriteHeard(line, spans)
		if !ok {
			continue
		}
		out = append(out, rewritten)
	}
	return out
}

// heardSpans maps each lowercased misheard span the matcher points at in text to the canonical
// word(s) it was matched to.
func heardSpans(text string, forms [][]string) map[string]map[string]struct{} {
	spans := make(map[string]map[string]struct{})
	for _, m := range MatchVocabulary(text, forms, DefaultVocabularyShortlist) {
		if m.Heard == "" || len(forms[m.Index]) == 0 {
			continue
		}
		addHeard(spans, strings.ToLower(m.Heard), strings.TrimSpace(forms[m.Index][0]))
	}
	return spans
}

func addHeard(spans map[string]map[string]struct{}, heard, word string) {
	if word == "" {
		return
	}
	if spans[heard] == nil {
		spans[heard] = make(map[string]struct{})
	}
	spans[heard][word] = struct{}{}
}

// sessionToken is one word of a session line and its byte range, split exactly as the matcher
// splits a transcript (foldedNgrams), so a matched span lines up with the words it came from.
type sessionToken struct {
	lower      string
	start, end int
}

func sessionTokens(line string) []sessionToken {
	var tokens []sessionToken
	start := -1
	for i, r := range line {
		sep := unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '\'' && r != '-')
		if sep {
			if start >= 0 {
				tokens = append(tokens, sessionToken{lower: strings.ToLower(line[start:i]), start: start, end: i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		tokens = append(tokens, sessionToken{lower: strings.ToLower(line[start:]), start: start, end: len(line)})
	}
	return tokens
}

// rewriteHeard replaces every whole-word occurrence of each span with its custom word, longest
// spans first so "cube netties" is rewritten before a single-word span could split it. Returns
// false when a span present in the line is claimed by more than one custom word.
func rewriteHeard(line string, spans map[string]map[string]struct{}) (string, bool) {
	if len(spans) == 0 {
		return line, true
	}
	keys := make([]string, 0, len(spans))
	for k := range spans {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		na, nb := len(strings.Fields(keys[a])), len(strings.Fields(keys[b]))
		if na != nb {
			return na > nb
		}
		return keys[a] < keys[b]
	})

	for _, heard := range keys {
		want := strings.Fields(heard)
		if len(want) == 0 {
			continue
		}
		tokens := sessionTokens(line)
		var b strings.Builder
		last := 0
		for i := 0; i+len(want) <= len(tokens); {
			match := true
			for j, w := range want {
				if tokens[i+j].lower != w {
					match = false
					break
				}
			}
			if !match {
				i++
				continue
			}
			words := spans[heard]
			if len(words) != 1 {
				return "", false
			}
			for w := range words {
				b.WriteString(line[last:tokens[i].start])
				b.WriteString(w)
			}
			last = tokens[i+len(want)-1].end
			i += len(want)
		}
		if last > 0 {
			b.WriteString(line[last:])
			line = b.String()
		}
	}
	return line, true
}
