package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	segDanglingEnd = setOf("a", "an", "the", "and", "or", "nor", "of", "for", "with", "from",
		"into", "if", "because", "than", "my", "our", "your", "their", "shows", "showed",
		"including", "per", "plus", "versus", "vs")
	segContinuingStart = setOf("and", "or", "nor", "than", "whom", "whose", "versus", "including")
	segLowerable       = setOf("a", "an", "the", "and", "or", "nor", "but", "so", "to", "of", "for",
		"with", "without", "from", "in", "on", "at", "by", "into", "after", "before", "when",
		"while", "then", "if", "because", "than", "as", "that", "this", "these", "those", "it",
		"its", "we", "he", "she", "they", "there", "his", "her", "our", "their", "my", "your",
		"which", "who", "whom", "whose", "what", "plus", "versus", "including", "also")
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[0], ",;:.!?")
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return strings.TrimRight(f[len(f)-1], ".,;:")
}

func spacelessLang(lang string) bool {
	l := strings.ToLower(lang)
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	switch l {
	case "zh", "ja", langYue, "th":
		return true
	}
	return false
}

func spacelessRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

func segmentSeparator(prev, next, lang string) string {
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(next)
	a, b := spacelessRune(last), spacelessRune(first)
	if (a && b) || (spacelessLang(lang) && (a || b)) {
		return ""
	}
	return " "
}

func joinSegments(texts []string, lang string) string {
	english := strings.HasPrefix(strings.ToLower(lang), "en")
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(t)
			continue
		}
		prev := b.String()
		next := strings.ToLower(firstWord(t))
		if english && strings.HasSuffix(prev, ".") && !strings.HasSuffix(prev, "..") &&
			(segDanglingEnd[strings.ToLower(lastWord(prev))] || segContinuingStart[next]) {
			b.Reset()
			b.WriteString(strings.TrimSuffix(prev, "."))
			if segLowerable[next] {
				r, size := utf8.DecodeRuneInString(t)
				t = string(unicode.ToLower(r)) + t[size:]
			}
		}
		b.WriteString(segmentSeparator(b.String(), t, lang))
		b.WriteString(t)
	}
	return b.String()
}
