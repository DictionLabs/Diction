package core

import "strings"

// plainTypography maps the invisible typographic characters gpt-oss writes when it
// rewrites a sentence onto the plain characters a keyboard produces. They render the
// same, but "on‑device" with U+2011 does not match a search for "on-device", and pasted
// into code or a terminal it is a different string. Measured 2026-09-24: once cleanup
// was allowed to repair grammar, one English dictation came back with 9 non-breaking
// hyphens and a narrow no-break space inside "Opus 5".
var plainTypography = strings.NewReplacer(
	"‐", "-", // hyphen
	"‑", "-", // non-breaking hyphen
	" ", " ", // no-break space
	" ", " ", // narrow no-break space
	" ", " ", // figure space
)

// PlainTypography returns s with LLM-only typographic characters replaced by their
// keyboard equivalents. Quotes and dashes are left alone: those are visible choices.
func PlainTypography(s string) string {
	return plainTypography.Replace(s)
}
