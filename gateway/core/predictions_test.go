package core

import (
	"strings"
	"testing"
)

func TestSanitizePredictions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"keeps order, caps at three", []string{"much", "kindly", "warmly", "dearly", "so"}, []string{"much", "kindly", "warmly"}},
		{"trims surrounding punctuation", []string{"much.", "\"kindly\"", "(warmly),"}, []string{"much", "kindly", "warmly"}},
		{"keeps inner apostrophes and hyphens", []string{"don't", "well-known", "'tis"}, []string{"don't", "well-known", "tis"}},
		{"drops multi-word and blank entries", []string{"thank you", "", "   ", "...", "much"}, []string{"much"}},
		{"dedupes case-insensitively, first wins", []string{"Much", "much", "MUCH", "kindly"}, []string{"Much", "kindly"}},
		{"a dropped entry lets a later one in", []string{"you", "you?", "doing", "fine", "well"}, []string{"you", "doing", "fine"}},
		{"keeps non-Latin words", []string{"pomoc,", "zprávu", "Ihre"}, []string{"pomoc", "zprávu", "Ihre"}},
		{"nil in, empty out", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizePredictions(tc.in)
			if got == nil || strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("SanitizePredictions(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
