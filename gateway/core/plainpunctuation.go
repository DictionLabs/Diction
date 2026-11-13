package core

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PlainPunctuation enforces the Clean up style rule "no em dashes, no semicolons" in code
// instead of in the prompt. As a prompt rule it made gpt-oss-120b write the whole answer in
// its hidden reasoning, check it for the banned marks and write it again, so reasoning grew
// with the dictation: measured 2026-10-18 on six medium/long dictations, dropping the rule cut
// mean reasoning 186 → 80 tokens and wall time 1.99 → 1.68 s (positive wording, "punctuate
// with commas and periods", still cost 181). An em dash, or a spaced en dash, becomes a comma;
// a semicolon ends the sentence. A mark the speaker dictated (the transcript already has it, or
// says "semicolon") is kept.
func PlainPunctuation(transcript, out string) string {
	if !strings.ContainsAny(transcript, "—") {
		out = replaceDashes(out)
	}
	if !strings.Contains(transcript, ";") && !strings.Contains(strings.ToLower(transcript), "semicolon") {
		out = replaceSemicolons(out)
	}
	return out
}

// lineDash is a dash opening a line, used as a list marker: it becomes the plain "- " bullet.
var lineDash = regexp.MustCompile(`(?m)^([ \t]*)(?:—|–)[ \t]*`)

// sentenceDash is an em dash anywhere, or an en dash with a space on at least one side (a bare
// en dash between digits, "5–10", is a range and stays).
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
			// nothing to join to
		case strings.ContainsRune(",.;:!?(", last):
			b.WriteString(" ")
		case rest == "" || strings.ContainsRune(",.;:!?)\n", next):
			// a trailing dash ("and then —.") just goes
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
