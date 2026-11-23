package main

import "testing"

// Explicit edit intents pass through, everything else is a dictation.
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
