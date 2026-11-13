package core

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NovelWords returns the words of output that none of sources heard: the merge's
// "chose, did not rewrite" test (diction-routes 01c), shared by the gateway's merge guard and
// prompt-eval's merge suite.
//
// Words are compared lowercased with diacritics folded. Words under 3 letters and anything
// with a digit are ignored (numbers become digits by design). A word still counts as heard
// when it is within a small edit distance of a source word (len/4, at least 1: a fixed ending
// or spelling, "minutu" → "minut") or equals two adjacent source words joined (a fixed word
// boundary, "je skyně" → "jeskyně"). Three more shapes count as heard, because the merge is
// asked to fix exactly these in inflected languages (Czech measurement 2026-10-12: 9 of 21
// rejected merges were these; the other 12 were invented words and stay flagged):
//   - stem: a shared prefix of ≥ 4 letters and at least half the word, differing only in
//     the ending (≤ 3 letters of the output's, ≤ 4 of the heard word's): "červeného" →
//     "červené", "narkovou" → "narkové";
//   - repair: a heard word with a middle chunk dropped (prefix ≥ 4 and suffix ≥ 2 cover the
//     whole output word): "kamenovalovány" → "kamenovány";
//   - split: the word and its neighbour joined are close to a heard word: "mostosauny" →
//     "mosty sauny".
//
// What is left is a word the model brought in.
func NovelWords(output string, sources ...string) []string {
	known := map[string]bool{}
	var knownList []string
	add := func(w string) {
		if !known[w] {
			known[w] = true
			knownList = append(knownList, w)
		}
	}
	for _, s := range sources {
		ws := foldedWords(s)
		for i, w := range ws {
			add(w)
			if i+1 < len(ws) {
				add(w + ws[i+1])
			}
		}
	}
	heardLike := func(w string) bool {
		r := []rune(w)
		maxDist := max(1, len(r)/4)
		for _, k := range knownList {
			kr := []rune(k)
			if abs(len(kr)-len(r)) <= maxDist && editDistanceWithin(r, kr, maxDist) {
				return true
			}
			if sameStem(r, kr) || droppedMiddle(r, kr) {
				return true
			}
		}
		return false
	}
	words := foldedWords(output)
	var novel []string
	seen := map[string]bool{}
	for i, w := range words {
		r := []rune(w)
		if len(r) < 3 || known[w] || seen[w] || strings.IndexFunc(w, unicode.IsDigit) >= 0 {
			continue
		}
		seen[w] = true
		if heardLike(w) {
			continue
		}
		if (i > 0 && splitOf(words[i-1], w, knownList)) || (i+1 < len(words) && splitOf(w, words[i+1], knownList)) {
			continue
		}
		novel = append(novel, w)
	}
	sort.Strings(novel)
	return novel
}

// sameStem: w and k share a prefix of ≥ 4 letters and ≥ half of w, and differ only in
// their endings (≤ 3 letters left of w, ≤ 4 of k).
func sameStem(w, k []rune) bool {
	p := commonPrefix(w, k)
	return p >= 4 && 2*p >= len(w) && len(w)-p <= 3 && len(k)-p <= 4
}

// droppedMiddle: w is k with a chunk from its middle removed (a garbled repeat repaired).
func droppedMiddle(w, k []rune) bool {
	if len(w) >= len(k) {
		return false
	}
	p := commonPrefix(w, k)
	s := 0
	for s < len(w) && s < len(k) && w[len(w)-1-s] == k[len(k)-1-s] {
		s++
	}
	return p >= 4 && s >= 2 && p+s >= len(w)
}

// splitOf reports whether two adjacent output words, each ≥ 3 letters, joined are within one
// edit of a heard word: a heard compound split into two words. (Short words are excluded:
// "je" + an invented word would too easily land near a heard "je…" word.)
func splitOf(a, b string, knownList []string) bool {
	if len([]rune(a)) < 3 || len([]rune(b)) < 3 {
		return false
	}
	r := []rune(a + b)
	for _, k := range knownList {
		kr := []rune(k)
		if abs(len(kr)-len(r)) <= 1 && editDistanceWithin(r, kr, 1) {
			return true
		}
	}
	return false
}

func commonPrefix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// foldedWords splits s into lowercase, diacritic-free words (letters and digits only).
func foldedWords(s string) []string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// editDistanceWithin reports whether the Levenshtein distance of a and b is ≤ limit.
func editDistanceWithin(a, b []rune, limit int) bool {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > limit {
			return false
		}
		prev, cur = cur, prev
	}
	return prev[len(b)] <= limit
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
