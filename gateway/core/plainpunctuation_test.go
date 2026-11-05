package core

import "testing"

func TestPlainPunctuation(t *testing.T) {
	cases := []struct {
		name, transcript, in, want string
	}{
		{"em dash pair", "x", "The demo — which was great — went well.", "The demo, which was great, went well."},
		{"unspaced em dash", "x", "It works—mostly.", "It works, mostly."},
		{"spaced en dash", "x", "We agreed – finally – on Friday.", "We agreed, finally, on Friday."},
		{"en dash range kept", "x", "Pages 5–10.", "Pages 5–10."},
		{"dash after comma", "x", "Well, — fine.", "Well, fine."},
		{"trailing dash", "x", "And then —.", "And then."},
		{"line dash bullet", "x", "Buy:\n— milk\n– eggs", "Buy:\n- milk\n- eggs"},
		{"semicolon ends sentence", "x", "That's fine; it costs one month.", "That's fine. It costs one month."},
		{"semicolon czech", "x", "Je to drahé; šetříme.", "Je to drahé. Šetříme."},
		{"semicolon at end", "x", "Done;", "Done."},
		{"dictated semicolon kept", "add a semicolon here", "a; b", "a; b"},
		{"transcript semicolon kept", "a; b", "a; b", "a; b"},
		{"dictated em dash kept", "a — b", "a — b", "a — b"},
		{"nothing to do", "x", "Plain text, nothing else.", "Plain text, nothing else."},
	}
	for _, c := range cases {
		if got := PlainPunctuation(c.transcript, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
