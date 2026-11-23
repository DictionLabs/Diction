package vocab

import (
	"sort"
	"strings"
	"unicode"
)

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
		var ok bool
		line, ok = rewriteOne(line, want, spans[heard])
		if !ok {
			return "", false
		}
	}
	return line, true
}

func tokensMatchAt(tokens []sessionToken, i int, want []string) bool {
	for j, w := range want {
		if tokens[i+j].lower != w {
			return false
		}
	}
	return true
}

func rewriteOne(line string, want []string, words map[string]struct{}) (string, bool) {
	tokens := sessionTokens(line)
	var b strings.Builder
	last := 0
	for i := 0; i+len(want) <= len(tokens); {
		if !tokensMatchAt(tokens, i, want) {
			i++
			continue
		}
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
	return line, true
}
