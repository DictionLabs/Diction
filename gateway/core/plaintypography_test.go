package core

import "testing"

func TestPlainTypography(t *testing.T) {
	cases := map[string]string{
		"on‑device, self‑hosted": "on-device, self-hosted",
		"something like Opus 5":  "something like Opus 5",
		"10 km":                  "10 km",
		"plain text stays":       "plain text stays",
		"we’re — fine":           "we’re — fine",
	}
	for in, want := range cases {
		if got := PlainTypography(in); got != want {
			t.Errorf("PlainTypography(%q) = %q, want %q", in, got, want)
		}
	}
}
