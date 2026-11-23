package transcript

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func PlainPunctuation(transcript, out string) string {
	if !strings.ContainsAny(transcript, "—") {
		out = replaceDashes(out)
	}
	if !strings.Contains(transcript, ";") && !strings.Contains(strings.ToLower(transcript), "semicolon") {
		out = replaceSemicolons(out)
	}
	return out
}

var lineDash = regexp.MustCompile(`(?m)^([ \t]*)(?:—|–)[ \t]*`)

var sentenceDash = regexp.MustCompile(`[ \t]*—[ \t]*|[ \t]+–[ \t]*|[ \t]*–[ \t]+`)

func replaceDashes(s string) string {
	s = lineDash.ReplaceAllString(s, "$1- ")
	idx := sentenceDash.FindAllStringIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, m := range idx {
		before := strings.TrimRight(s[prev:m[0]], " \t")
		b.WriteString(before)
		rest := s[m[1]:]
		last, _ := utf8.DecodeLastRuneInString(b.String())
		next, _ := utf8.DecodeRuneInString(rest)
		switch {
		case b.Len() == 0 || last == '\n':
		case strings.ContainsRune(",.;:!?(", last):
			b.WriteString(" ")
		case rest == "" || strings.ContainsRune(",.;:!?)\n", next):
		default:
			b.WriteString(", ")
		}
		prev = m[1]
	}
	b.WriteString(s[prev:])
	return b.String()
}

func replaceSemicolons(s string) string {
	if !strings.Contains(s, ";") {
		return s
	}
	var b strings.Builder
	upper := false
	for _, r := range s {
		if r == ';' {
			b.WriteRune('.')
			upper = true
			continue
		}
		if upper && unicode.IsLetter(r) {
			r = unicode.ToUpper(r)
			upper = false
		} else if upper && !unicode.IsSpace(r) {
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
