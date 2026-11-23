package transcript

import (
	"strings"
	"testing"
)

func TestHasDegenerateRepetition(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"normal sentence", "the quick brown fox jumps over the lazy dog", false},
		{"short plausible repeat", "no no no no no way", false},
		{"exact threshold run", strings.Repeat("tamb ", degenerateRepetitionThreshold), true},
		{"just under threshold", strings.Repeat("tamb ", degenerateRepetitionThreshold-1), false},
		{"mixed case and punctuation still collapses", strings.Repeat("Tamb, ", degenerateRepetitionThreshold), true},
		{"long text without a run", strings.Repeat("alpha beta gamma delta ", 10), false},
		{"run broken up short of threshold", strings.Repeat("tamb tamb tamb foo ", 5), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDegenerateRepetition(c.text); got != c.want {
				t.Errorf("hasDegenerateRepetition(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
