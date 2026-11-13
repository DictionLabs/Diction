package core

import (
	"sort"
	"strings"
	"unicode"
)

// Per-utterance vocabulary ranking: which of the user's custom words are worth putting in
// front of the cleanup LLM for *this* transcript.
//
// The literature is unambiguous that sending the whole list is worse than sending nothing
// (see .claude/RARE_WORD_ASR_RESEARCH.md §1): a bare vocabulary list raises error on ordinary
// words, and an LLM given many similar spellings picks between them no better than chance.
// Every strong published result shortlists per utterance, by *phonetic* distance, and around
// ten candidates is the ceiling for a prompted model.
//
// So this is retrieval, not truncation. A word earns its place in the prompt only when
// something in the transcript actually sounds like it.

// DefaultVocabularyShortlist is how many custom words may reach the cleanup prompt for one
// utterance. Ten is the measured ceiling for a prompted LLM (Lei et al. 2024: best at 10,
// worse at 20).
const DefaultVocabularyShortlist = 10

const (
	// Distance below which a form is admitted regardless of what else matched — a near-exact
	// phonetic hit.
	vocabStrongDistance = 0.2
	// A form within this multiple of the best distance *for the same stretch of transcript*
	// rides along with it, so a genuine ambiguity ("Machala" vs "Machova") reaches the LLM as
	// a small set rather than a coin-flip already decided here. Scoped per span since
	// 2026-09-24: measured against the best of the whole utterance, one exact hit ("Fennec")
	// set the bar to zero and silently dropped every other name the user said.
	vocabRelativeSlack = 1.2
	// ...but only when the best match is itself credible. Past this, nothing in the
	// transcript really sounds like anything in the list.
	vocabBestDistanceCeiling = 0.35
	// Longest n-gram of transcript tokens compared against a single vocabulary form, so
	// multi-word manglings ("cube netties" → Kubernetes) can be matched.
	vocabMaxNgram = 3
	// Shortest form worth scoring, mirroring MyWordsCorrector.minTokenLength: edit distance
	// is unreliable on very short strings, and a two-letter word would match half the list.
	vocabMinFormLength = 3
	// Shortest *folded* string worth scoring, on either side. Folding drops non-leading
	// vowels, so a fold of two runes carries almost no signal: without this floor "is" in an
	// ordinary sentence scores 0.33 against "Iris" and drags a name into the prompt.
	vocabMinFolded = 3
	// Shortest folded form allowed a fuzzy match. Below it, one edit is a third of the key,
	// which is within the ceiling, and three consonants one letter off describe half the
	// language: "Kuiil" ("kvl") matched "we will" ("vvl") and the LLM wrote "Kuiil put home
	// TLD". A form this short must fold to exactly what was heard, as "Quill" does.
	vocabMinFuzzyFolded = 4
	// Spelling distance, vowels kept (letterKey), at which a sound-key hit is only a shared
	// consonant skeleton. See requireCorroboration.
	vocabWeakSpelling = 0.5
	// Ceiling on entries considered, so an unbounded client list cannot turn one request into
	// a quadratic scan.
	vocabMaxEntries = 2000
)

// RankVocabulary picks the custom words worth showing the LLM for this transcript.
//
// forms[i] is one entry's spellings: forms[i][0] is the canonical word and the rest are its
// known variants (the misheard forms the wire already carries). An entry scores as its best
// form, so a stored mishearing is an exact hit rather than a phonetic guess — which is the
// whole point of keeping variants.
//
// Returns indices into forms, ordered weakest match first so the strongest sits *last* in the
// prompt: LLMs weight the end of a list most (Hou et al. 2025 measured ascending 5.73 vs
// descending 10.03 B-WER). Returns nil when nothing is close enough, which is a valid and
// common answer — the caller then omits the block entirely.
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

// VocabularyMatch is one shortlisted entry and the transcript words it matched.
type VocabularyMatch struct {
	// Index into the forms passed to MatchVocabulary.
	Index int
	// The transcript words, as written, that sound like the entry — "McCullough" for Machala.
	// Empty when they already are the entry's canonical spelling, since there is nothing to
	// point at. Telling the LLM *where* the match is matters: shown only the word list,
	// gpt-oss-120b judged "Andre McCullough" a real name and kept it 0/4; shown the span, 4/4.
	Heard string
}

// vocabCandidate is one custom word's best hit in the transcript, before admission.
type vocabCandidate struct {
	index    int
	distance float64
	// The transcript tokens [start, end) the winning hit covered, for per-span slack.
	start, end int
	heard      string
	// taught: heard is one of the entry's stored variants, spelled as the user saved it.
	taught bool
	// letters is the normalized edit distance between the spellings with every letter kept
	// (letterKey), where distance compares sound keys with the vowels dropped.
	letters float64
}

// MatchVocabulary is RankVocabulary plus, for each admitted entry, the transcript words it
// matched. Same admission, same order.
func MatchVocabulary(transcript string, forms [][]string, limit int) []VocabularyMatch {
	return MatchVocabularyWith(transcript, forms, limit, VocabularyOptions{})
}

// VocabularyOptions says what the gateway knows about where the transcript came from.
type VocabularyOptions struct {
	// STTBoosted: the STT backend that wrote the transcript was sent the custom-word list
	// (Backend.AcceptsCustomWords: Cohere, Canary, Parakeet). Since 2026-11-05 the shortlist no
	// longer treats this differently: a boosted STT that writes "Christina" for a saved
	// "Kristýna" has not proven Christina was said (see requireCorroboration).
	STTBoosted bool
}

// MatchVocabularyWith is MatchVocabulary for a transcript whose origin is known.
func MatchVocabularyWith(transcript string, forms [][]string, limit int, opts VocabularyOptions) []VocabularyMatch {
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
		// Two minima, kept apart on purpose. `ordinary` is the closest ordinary word of the
		// language this entry resembles; `other` is the closest anything else. A custom word
		// whose best likeness is an ordinary word is not a mis-transcription — "Parker" next
		// to "park" — while one that only resembles an odd sequence is exactly what we are
		// looking for: "Vsetín" next to "set in".
		ordinary, other := 1.0, 1.0
		var ordinaryHit, otherHit transcriptNgram
		shortLetters := 1.0
		for _, form := range entry {
			folded := foldPhonetic(form)
			if len([]rune(strings.TrimSpace(form))) < vocabMinFormLength {
				continue
			}
			if len([]rune(folded)) < vocabMinFolded {
				if d, hit, ok := matchShortForm(form, folded, ngrams); ok && d < other {
					other, otherHit, shortLetters = d, hit, d
				}
				continue
			}
			for _, ngram := range ngrams {
				if ngram.short {
					continue
				}
				// Cheap rejection before the O(n·m) DP: two strings whose lengths already
				// differ by more than the ceiling can never score under it, and this runs on
				// the request path for every entry × form × n-gram.
				if lengthRatioExceeds(folded, ngram.folded, vocabBestDistanceCeiling) {
					continue
				}
				d := normalizedLevenshtein(folded, ngram.folded)
				if d > 0 && len([]rune(folded)) < vocabMinFuzzyFolded {
					continue
				}
				if ngram.ordinary {
					if d < ordinary {
						ordinary, ordinaryHit = d, ngram
					}
					continue
				}
				if d < other {
					other, otherHit = d, ngram
				}
			}
		}

		distance, hit := other, otherHit
		if ordinary == 0 {
			// The entry *is* that ordinary word. An exact phonetic identity is never an
			// over-correction, so a My Word like "Park" keeps working.
			distance, hit = 0, ordinaryHit
		} else if ordinary <= other {
			continue
		}
		if distance > vocabBestDistanceCeiling {
			continue
		}
		heard := hit.text
		if strings.EqualFold(heard, strings.TrimSpace(entry[0])) || allOrdinaryWords(heard) {
			heard = ""
		}
		letters := shortLetters
		for _, form := range entry {
			if d := normalizedLevenshtein(letterKey(form), letterKey(hit.text)); d < letters {
				letters = d
			}
		}
		candidates = append(candidates, vocabCandidate{
			index: i, distance: distance, start: hit.start, end: hit.end, heard: heard,
			taught:  heard != "" && isStoredVariant(heard, entry[1:]),
			letters: letters,
		})
	}

	if len(candidates) == 0 {
		return nil
	}

	// Each candidate is judged against the best candidate competing for an overlapping stretch
	// of the transcript, never against the utterance as a whole: several names in one sentence
	// are several mentions, not rivals.
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

	admitted = requireCorroboration(admitted)

	// Weakest first: a stable sort keeps input order among equal distances, so the same list
	// and transcript always render the same prompt.
	sort.SliceStable(admitted, func(a, b int) bool {
		return admitted[a].distance > admitted[b].distance
	})
	if len(admitted) > limit {
		// Keep the closest, which are at the tail.
		admitted = admitted[len(admitted)-limit:]
	}

	out := make([]VocabularyMatch, 0, len(admitted))
	for _, c := range admitted {
		out = append(out, VocabularyMatch{Index: c.index, Heard: c.heard})
	}
	return out
}

// requireCorroboration drops hits that share only a consonant skeleton with the custom word
// (2026-10-18 Writing Tools e2e runs, .claude/bow/writing-tools-mywords-cooperation-2026-10.md).
// The sound key drops every non-leading vowel, so "Thursday"/tirzepatide, "I can"/Joaquín,
// "keyboard is"/Kubernetes and Czech "přes"/Apiaries all scored under the ceiling, and the LLM
// treats a (sounds like "X" here) pointer as an instruction. With the vowels kept they are ≥ 0.5
// apart, while every real mishearing measured was ≤ 0.45 (Sapansky/Szczepański, Matapolo/
// metoprolol, Christina/Kristýna 0.33, Andre/Ondřej 0.33). Such a weak hit stays only when the
// user taught it as a variant, or next to another misheard span: a garbled full name ("Andre
// McCullough") is two adjacent manglings, which a coincidence almost never is.
//
// Every hit that passes keeps its pointer, a lone real name included. Withdrawn 2026-11-05: two
// further rules from 2026-10-18 stripped the pointer from a lone single word and, when the STT
// was boosted, dropped the entry when the heard word was itself a dictionary word. Together they
// meant a user who saved "Kristýna" or "Ondřej" and said it never got it ("Christina", "Andre":
// shortlist 0 on every boosted dictation). Product decision: a word the user saved and said wins
// over a different real name of the same sound; the LLM gets the pointer and the sentence, and
// the user who means another person can say so in the sentence or edit the result.
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

// misheardNeighbour reports whether another admitted candidate with a pointer sits directly next
// to admitted[i] in the transcript.
func misheardNeighbour(admitted []vocabCandidate, i int) bool {
	c := admitted[i]
	for j, o := range admitted {
		if j != i && (o.start == c.end || o.end == c.start) && o.heard != "" {
			return true
		}
	}
	return false
}

// matchShortForm scores a custom word whose sound key is too short for the main matcher
// (two consonants: Cara "kr", rig "rg"). Such a key one edit off describes half the language,
// so the bar is far stricter: one transcript word, not an ordinary word of the language, with a
// sound key of the same length, the same first letter, the same vowels, and at most one letter
// apart with the vowels kept (shortKey). Kara → Cara (identical), Rick → rig ("rik"/"rig"), but
// not "rug" or "Cora". Returns the normalized shortKey distance.
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

// vowels returns the vowels of s in order.
func vowels(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isFoldableVowel(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shortKey is foldPhonetic with the vowels kept: diacritics stripped, the same spelling
// rewrites (ck → k, soft/hard c, ...), doubled letters merged.
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

// allOrdinaryWords reports whether every token of a matched span is an ordinary word of the
// language. Such a span ("on the door" for Ondrej) is admitted but gets no pointer: the note
// reads to the LLM as an instruction, and at low reasoning effort it swapped the name in even
// when that broke the sentence ("I left the keys Ondrej"). Without the pointer the LLM still
// sees the word and decides from the sentence's meaning.
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

// transcriptNgram is one folded window of the transcript, plus whether it is a single ordinary
// word of the language and therefore protected from substitution. start/end are the token
// indices [start, end) of the window's first occurrence (repeats are deduplicated), and text
// is that window as written.
type transcriptNgram struct {
	folded     string
	ordinary   bool
	start, end int
	text       string
	// short: a single token whose sound key is below vocabMinFolded, scored only by
	// matchShortForm.
	short bool
}

// foldedNgrams renders the transcript as folded 1..vocabMaxNgram token windows, so a single
// vocabulary word can match a phrase the STT split into several ("recording life cycle").
func foldedNgrams(transcript string) []transcriptNgram {
	tokens := strings.FieldsFunc(transcript, func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '\'' && r != '-')
	})
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tokens)*vocabMaxNgram)
	var out []transcriptNgram
	for n := 1; n <= vocabMaxNgram; n++ {
		for i := 0; i+n <= len(tokens); i++ {
			text := strings.Join(tokens[i:i+n], " ")
			folded := foldPhonetic(text)
			if len([]rune(folded)) < vocabMinFolded {
				if n == 1 && len([]rune(folded)) > 0 {
					key := "short:" + strings.ToLower(text)
					if _, dup := seen[key]; !dup {
						seen[key] = struct{}{}
						_, ordinary := commonEnglishWords[strings.ToLower(text)]
						out = append(out, transcriptNgram{folded: folded, ordinary: ordinary, start: i, end: i + 1, text: text, short: true})
					}
				}
				continue
			}
			if _, dup := seen[folded]; dup {
				continue
			}
			seen[folded] = struct{}{}
			ordinary := false
			if n == 1 {
				_, ordinary = commonEnglishWords[strings.ToLower(tokens[i])]
			}
			out = append(out, transcriptNgram{folded: folded, ordinary: ordinary, start: i, end: i + n, text: text})
		}
	}
	return out
}

// foldPhonetic reduces a string to a rough sound key: diacritics stripped, spelling
// differences that sound alike collapsed, doubled letters merged, and non-leading vowels
// dropped. Deliberately language-neutral and deliberately crude — it decides only *which*
// words are worth showing the LLM, and the LLM still sees the real spelling.
//
// No golang.org/x/text: this file lives in core/, which is copied verbatim into the community
// gateway at release, and that module does not carry the dependency.
func foldPhonetic(s string) string {
	lowered := strings.ToLower(strings.TrimSpace(s))
	var stripped strings.Builder
	stripped.Grow(len(lowered))
	for _, r := range lowered {
		switch r {
		case 'ß':
			stripped.WriteString("ss")
		case 'æ':
			stripped.WriteString("ae")
		case 'œ':
			stripped.WriteString("oe")
		case 'ø':
			stripped.WriteRune('o')
		case 'ł':
			stripped.WriteRune('l')
		case 'đ', 'ð':
			stripped.WriteRune('d')
		case 'þ':
			stripped.WriteString("th")
		default:
			if folded, ok := diacriticFolding[r]; ok {
				stripped.WriteRune(folded)
				continue
			}
			if unicode.IsLetter(r) || unicode.IsSpace(r) {
				stripped.WriteRune(r)
			}
		}
	}

	folded := dropSilentGH(stripped.String())
	// Ordered: longer digraphs first, so "sch" is not eaten by "ch".
	for _, rule := range phoneticRewrites {
		folded = strings.ReplaceAll(folded, rule.from, rule.to)
	}
	folded = softCBeforeFrontVowel(folded)
	folded = kuBeforeVowel(folded)

	var out strings.Builder
	out.Grow(len(folded))
	var previous rune
	for i, r := range folded {
		if r == ' ' {
			continue
		}
		if r == previous {
			continue // doubled letters sound single
		}
		if i > 0 && isFoldableVowel(r) {
			previous = r
			continue // vowels carry little signal away from the first letter
		}
		out.WriteRune(r)
		previous = r
	}
	return out.String()
}

// dropSilentGH removes "gh" everywhere except at the start of a word. In English a non-initial
// gh is silent ("night", "McCullough") or an /f/ ("tough") and never the /g/+/h/ the letters
// suggest, so keeping it made an English-spoken "Machala" transcribed as "McCullough" land 0.60
// away instead of 0.25. Word-initial gh ("ghost") keeps its /g/.
func dropSilentGH(s string) string {
	if !strings.Contains(s, "gh") {
		return s
	}
	runes := []rune(s)
	var out strings.Builder
	out.Grow(len(runes))
	for i := 0; i < len(runes); i++ {
		if runes[i] == 'g' && i+1 < len(runes) && runes[i+1] == 'h' && i > 0 && runes[i-1] != ' ' {
			i++
			continue
		}
		out.WriteRune(runes[i])
	}
	return out.String()
}

// Deliberately no "ch" rule. English and Czech disagree about it — "Christina" opens with a
// /k/, "Machala" with an /x/ — so any single mapping breaks one of the two names this feature
// exists for. Leaving the h in place lets edit distance absorb the difference instead:
// Christina/Kristýna land 0.17 apart and Machala/Mahala 0.25, both admitted, where "ch"→"h"
// put Christina and Kristýna in different places entirely.
var phoneticRewrites = []struct{ from, to string }{
	{"sch", "s"},
	{"ph", "f"},
	{"ck", "k"},
	{"th", "t"},
	{"qu", "kv"},
	{"sh", "s"},
	{"x", "ks"},
	{"w", "v"},
	{"y", "i"},
	{"j", "i"},
	{"z", "s"},
}

// softCBeforeFrontVowel maps c → s before e/i/y and c → k elsewhere, the one context-sensitive
// rule worth having: it is what makes "Christina" and "Kristýna" meet.
func softCBeforeFrontVowel(s string) string {
	runes := []rune(s)
	var out strings.Builder
	out.Grow(len(runes))
	for i, r := range runes {
		if r != 'c' {
			out.WriteRune(r)
			continue
		}
		next := ' '
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if next == 'e' || next == 'i' {
			out.WriteRune('s')
			continue
		}
		out.WriteRune('k')
	}
	return out.String()
}

// kuBeforeVowel maps "ku" before a vowel onto "kv", the same key "qu" gets: "Kuiil" is said
// /kwil/ and heard as "Quill". Without it the u is dropped as a vowel, "Kuiil" folds to a
// two-letter "kl" below vocabMinFolded, and the name can never be matched at all. Runs after
// softCBeforeFrontVowel so a hard c ("cuisine" → "kuisine") is covered too.
func kuBeforeVowel(s string) string {
	if !strings.Contains(s, "ku") {
		return s
	}
	runes := []rune(s)
	for i := 0; i+2 < len(runes); i++ {
		if runes[i] == 'k' && runes[i+1] == 'u' && isFoldableVowel(runes[i+2]) && runes[i+2] != 'u' {
			runes[i+1] = 'v'
		}
	}
	return string(runes)
}

func isFoldableVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// lengthRatioExceeds reports whether the length difference alone already puts two strings
// further apart than `limit`. The edit distance is at least the length difference, so this is a
// sound skip, never a heuristic one.
func lengthRatioExceeds(a, b string, limit float64) bool {
	la, lb := len([]rune(a)), len([]rune(b))
	longest, diff := la, la-lb
	if lb > longest {
		longest = lb
	}
	if diff < 0 {
		diff = -diff
	}
	if longest == 0 {
		return false
	}
	return float64(diff)/float64(longest) > limit
}

// normalizedLevenshtein is the edit distance divided by the longer length, so 0 is identical
// and 1 is entirely different regardless of word length.
func normalizedLevenshtein(a, b string) float64 {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 1
	}
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	longest := len(ra)
	if len(rb) > longest {
		longest = len(rb)
	}
	return float64(previous[len(rb)]) / float64(longest)
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// diacriticFolding maps accented Latin letters onto their base letter. Generated from the
// Unicode decompositions of Latin-1 Supplement and Latin Extended-A, written out literally so
// core/ stays free of golang.org/x/text (see foldPhonetic). Lowercase keys only — foldPhonetic
// lowercases before it looks anything up.
var diacriticFolding = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'ç': 'c', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ì': 'i',
	'í': 'i', 'î': 'i', 'ï': 'i', 'ñ': 'n', 'ò': 'o', 'ó': 'o',
	'ô': 'o', 'õ': 'o', 'ö': 'o', 'ù': 'u', 'ú': 'u', 'û': 'u',
	'ü': 'u', 'ý': 'y', 'ÿ': 'y', 'ā': 'a', 'ă': 'a', 'ą': 'a',
	'ć': 'c', 'ĉ': 'c', 'ċ': 'c', 'č': 'c', 'ď': 'd', 'ē': 'e',
	'ĕ': 'e', 'ė': 'e', 'ę': 'e', 'ě': 'e', 'ĝ': 'g', 'ğ': 'g',
	'ġ': 'g', 'ģ': 'g', 'ĥ': 'h', 'ĩ': 'i', 'ī': 'i', 'ĭ': 'i',
	'į': 'i', 'ĵ': 'j', 'ķ': 'k', 'ĺ': 'l', 'ļ': 'l', 'ľ': 'l',
	'ń': 'n', 'ņ': 'n', 'ň': 'n', 'ō': 'o', 'ŏ': 'o', 'ő': 'o',
	'ŕ': 'r', 'ŗ': 'r', 'ř': 'r', 'ś': 's', 'ŝ': 's', 'ş': 's',
	'š': 's', 'ţ': 't', 'ť': 't', 'ũ': 'u', 'ū': 'u', 'ŭ': 'u',
	'ů': 'u', 'ű': 'u', 'ų': 'u', 'ŵ': 'w', 'ŷ': 'y', 'ź': 'z',
	'ż': 'z', 'ž': 'z',
}

// CustomWordLine renders one shortlisted entry for the <custom_words> prompt block. Shared by
// the gateway and prompt-eval so the eval measures the exact text production sends.
func CustomWordLine(word string, variants []string, heard string) string {
	var notes []string
	if len(variants) > 0 {
		notes = append(notes, "also heard as: "+strings.Join(variants, ", "))
	}
	if heard != "" {
		notes = append(notes, `sounds like "`+heard+`" here`)
	}
	if len(notes) == 0 {
		return word
	}
	return word + " (" + strings.Join(notes, "; ") + ")"
}

// letterKey is a spelling with case, diacritics, spaces and punctuation removed but every
// letter, vowels included, kept.
func letterKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := diacriticFolding[r]; ok {
			r = f
		}
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isStoredVariant(heard string, variants []string) bool {
	for _, v := range variants {
		if strings.EqualFold(strings.TrimSpace(v), heard) {
			return true
		}
	}
	return false
}
