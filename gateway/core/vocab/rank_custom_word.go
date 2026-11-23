package vocab

import (
	"strings"
	"unicode"
)

func CustomWordLine(word string, variants []string, heard string) string {
	var notes []string
	if len(variants) > 0 {
		notes = append(notes, "also heard as: "+strings.Join(variants, ", "))
	}
	if heard != "" {
		notes = append(notes, `sounds like "`+heard+`" here`)
	}
	if len(notes) == 0 {
		return word
	}
	return word + " (" + strings.Join(notes, "; ") + ")"
}

func letterKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := diacriticFolding[r]; ok {
			r = f
		}
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isStoredVariant(heard string, variants []string) bool {
	for _, v := range variants {
		if strings.EqualFold(strings.TrimSpace(v), heard) {
			return true
		}
	}
	return false
}
