package transcript

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func knownWordList(sources []string) (known map[string]bool, knownList []string) {
	known = map[string]bool{}
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
	return known, knownList
}

func heardLike(w string, knownList []string) bool {
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

func NovelWords(output string, sources ...string) []string {
	known, knownList := knownWordList(sources)
	words := foldedWords(output)
	var novel []string
	seen := map[string]bool{}
	for i, w := range words {
		r := []rune(w)
		if len(r) < 3 || known[w] || seen[w] || strings.IndexFunc(w, unicode.IsDigit) >= 0 {
			continue
		}
		seen[w] = true
		if heardLike(w, knownList) {
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

func sameStem(w, k []rune) bool {
	p := commonPrefix(w, k)
	return p >= 4 && 2*p >= len(w) && len(w)-p <= 3 && len(k)-p <= 4
}

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
