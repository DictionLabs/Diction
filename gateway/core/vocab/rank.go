package vocab

import (
	"sort"
	"strings"
)

const DefaultVocabularyShortlist = 10

const (
	vocabStrongDistance      = 0.2
	vocabRelativeSlack       = 1.2
	vocabBestDistanceCeiling = 0.35
	vocabMaxNgram            = 3
	vocabMinFormLength       = 3
	vocabMinFolded           = 3
	vocabMinFuzzyFolded      = 4
	vocabWeakSpelling        = 0.5
	vocabMaxEntries          = 2000
)

func RankVocabulary(transcript string, forms [][]string, limit int) []int {
	matches := MatchVocabulary(transcript, forms, limit)
	if matches == nil {
		return nil
	}
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Index)
	}
	return out
}

type VocabularyMatch struct {
	Index int
	Heard string
}

type vocabCandidate struct {
	index      int
	distance   float64
	start, end int
	heard      string
	taught     bool
	letters    float64
}

func MatchVocabulary(transcript string, forms [][]string, limit int) []VocabularyMatch {
	return MatchVocabularyWith(transcript, forms, limit, VocabularyOptions{})
}

type VocabularyOptions struct {
	STTBoosted bool
}

func MatchVocabularyWith(transcript string, forms [][]string, limit int, _ VocabularyOptions) []VocabularyMatch {
	if limit <= 0 || len(forms) == 0 {
		return nil
	}
	ngrams := foldedNgrams(transcript)
	if len(ngrams) == 0 {
		return nil
	}

	var candidates []vocabCandidate

	entries := forms
	if len(entries) > vocabMaxEntries {
		entries = entries[:vocabMaxEntries]
	}
	for i, entry := range entries {
		if c, ok := scoreEntry(i, entry, ngrams); ok {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	admitted := admitCandidates(candidates)

	admitted = requireCorroboration(admitted)

	sort.SliceStable(admitted, func(a, b int) bool {
		return admitted[a].distance > admitted[b].distance
	})
	if len(admitted) > limit {
		admitted = admitted[len(admitted)-limit:]
	}

	out := make([]VocabularyMatch, 0, len(admitted))
	for _, c := range admitted {
		out = append(out, VocabularyMatch{Index: c.index, Heard: c.heard})
	}
	return out
}

type formScan struct {
	ordinary, other, shortLetters float64
	ordinaryHit, otherHit         transcriptNgram
}

func scanForms(entry []string, ngrams []transcriptNgram) formScan {
	s := formScan{ordinary: 1.0, other: 1.0, shortLetters: 1.0}
	for _, form := range entry {
		folded := foldPhonetic(form)
		if len([]rune(strings.TrimSpace(form))) < vocabMinFormLength {
			continue
		}
		if len([]rune(folded)) < vocabMinFolded {
			if d, hit, ok := matchShortForm(form, folded, ngrams); ok && d < s.other {
				s.other, s.otherHit, s.shortLetters = d, hit, d
			}
			continue
		}
		s.scanNgrams(folded, ngrams)
	}
	return s
}

func (s *formScan) scanNgrams(folded string, ngrams []transcriptNgram) {
	for _, ngram := range ngrams {
		if ngram.short {
			continue
		}
		if lengthRatioExceeds(folded, ngram.folded, vocabBestDistanceCeiling) {
			continue
		}
		d := normalizedLevenshtein(folded, ngram.folded)
		if d > 0 && len([]rune(folded)) < vocabMinFuzzyFolded {
			continue
		}
		if ngram.ordinary {
			if d < s.ordinary {
				s.ordinary, s.ordinaryHit = d, ngram
			}
			continue
		}
		if d < s.other {
			s.other, s.otherHit = d, ngram
		}
	}
}

func scoreEntry(i int, entry []string, ngrams []transcriptNgram) (vocabCandidate, bool) {
	scan := scanForms(entry, ngrams)

	distance, hit := scan.other, scan.otherHit
	if scan.ordinary == 0 {
		distance, hit = 0, scan.ordinaryHit
	} else if scan.ordinary <= scan.other {
		return vocabCandidate{}, false
	}
	if distance > vocabBestDistanceCeiling {
		return vocabCandidate{}, false
	}
	heard := hit.text
	if strings.EqualFold(heard, strings.TrimSpace(entry[0])) || allOrdinaryWords(heard) {
		heard = ""
	}
	letters := scan.shortLetters
	for _, form := range entry {
		if d := normalizedLevenshtein(letterKey(form), letterKey(hit.text)); d < letters {
			letters = d
		}
	}
	return vocabCandidate{
		index: i, distance: distance, start: hit.start, end: hit.end, heard: heard,
		taught:  heard != "" && isStoredVariant(heard, entry[1:]),
		letters: letters,
	}, true
}

func admitCandidates(candidates []vocabCandidate) []vocabCandidate {
	admitted := make([]vocabCandidate, 0, len(candidates))
	for _, c := range candidates {
		localBest := c.distance
		for _, o := range candidates {
			if o.start < c.end && c.start < o.end && o.distance < localBest {
				localBest = o.distance
			}
		}
		if c.distance <= vocabStrongDistance || c.distance <= vocabRelativeSlack*localBest {
			admitted = append(admitted, c)
		}
	}
	return admitted
}

func requireCorroboration(admitted []vocabCandidate) []vocabCandidate {
	kept := make([]vocabCandidate, 0, len(admitted))
	for i, c := range admitted {
		if c.letters >= vocabWeakSpelling && !c.taught && !misheardNeighbour(admitted, i) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func misheardNeighbour(admitted []vocabCandidate, i int) bool {
	c := admitted[i]
	for j, o := range admitted {
		if j != i && (o.start == c.end || o.end == c.start) && o.heard != "" {
			return true
		}
	}
	return false
}

func matchShortForm(form, folded string, ngrams []transcriptNgram) (float64, transcriptNgram, bool) {
	key := shortKey(form)
	keyRunes := []rune(key)
	best, found := 1.0, false
	var bestHit transcriptNgram
	for _, ngram := range ngrams {
		if !ngram.short || ngram.ordinary || len([]rune(ngram.folded)) != len([]rune(folded)) {
			continue
		}
		heard := shortKey(ngram.text)
		heardRunes := []rune(heard)
		if len(heardRunes) == 0 || len(keyRunes) == 0 || heardRunes[0] != keyRunes[0] ||
			vowels(heard) != vowels(key) {
			continue
		}
		d := normalizedLevenshtein(key, heard)
		longest := len(keyRunes)
		if len(heardRunes) > longest {
			longest = len(heardRunes)
		}
		if d*float64(longest) > 1.0001 || d > vocabBestDistanceCeiling {
			continue
		}
		if d < best {
			best, bestHit, found = d, ngram, true
		}
	}
	return best, bestHit, found
}

func vowels(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isFoldableVowel(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func shortKey(s string) string {
	l := letterKey(s)
	for _, rule := range phoneticRewrites {
		l = strings.ReplaceAll(l, rule.from, rule.to)
	}
	l = softCBeforeFrontVowel(l)
	var b strings.Builder
	var prev rune
	for _, r := range l {
		if r != prev {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

func allOrdinaryWords(span string) bool {
	tokens := strings.Fields(span)
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if _, ok := commonEnglishWords[strings.ToLower(t)]; !ok {
			return false
		}
	}
	return true
}
