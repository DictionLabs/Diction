package main

import "testing"

// The whole point of modeForIntent is that there is exactly one of it. This table is small
// because the rule is small: explicit edit intents pass through, everything else is a
// dictation. The gateway never infers an edit from what the user said.
func TestModeForIntent(t *testing.T) {
	cases := map[string]string{
		"edit":          "edit",
		"edit-selected": "edit-selected",
		"":              "transcribe",
		"transcribe":    "transcribe",
		"nonsense":      "transcribe",
	}
	for intent, want := range cases {
		if got := modeForIntent(intent); got != want {
			t.Errorf("modeForIntent(%q) = %q, want %q", intent, got, want)
		}
	}
}
