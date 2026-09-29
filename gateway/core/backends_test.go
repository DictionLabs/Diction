package core

import "testing"

// TestLangSet_ReturnsCopy asserts langSet never hands out a live reference to
// a package-level language set. Nothing mutates euLanguages/cohereLanguages
// today (DefaultBackends() runs once at startup), so this guards against a
// future bug rather than a present one — see langSet's doc comment.
func TestLangSet_ReturnsCopy(t *testing.T) {
	src := map[string]bool{"en": true, "fr": true}
	got := langSet(src)

	got["de"] = true
	delete(got, "en")

	if !src["en"] {
		t.Error("mutating langSet's result deleted a key from the source map")
	}
	if src["de"] {
		t.Error("mutating langSet's result added a key to the source map")
	}
	if len(src) != 2 {
		t.Errorf("source map length changed: got %d, want 2", len(src))
	}
}

func TestLangSet_EULanguagesIndependent(t *testing.T) {
	before := len(euLanguages)
	copy1 := langSet(euLanguages)
	copy1["xx"] = true

	if len(euLanguages) != before {
		t.Errorf("mutating a langSet(euLanguages) copy changed euLanguages: len %d, want %d", len(euLanguages), before)
	}
}
