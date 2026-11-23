package vocab

import (
	"strings"
	"unicode"
)

type transcriptNgram struct {
	folded     string
	ordinary   bool
	start, end int
	text       string
	short      bool
}

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
			if g, ok := ngramAt(tokens, i, n, seen); ok {
				out = append(out, g)
			}
		}
	}
	return out
}

func ngramAt(tokens []string, i, n int, seen map[string]struct{}) (transcriptNgram, bool) {
	text := strings.Join(tokens[i:i+n], " ")
	folded := foldPhonetic(text)
	if len([]rune(folded)) < vocabMinFolded {
		if n != 1 || len([]rune(folded)) == 0 {
			return transcriptNgram{}, false
		}
		key := "short:" + strings.ToLower(text)
		if _, dup := seen[key]; dup {
			return transcriptNgram{}, false
		}
		seen[key] = struct{}{}
		_, ordinary := commonEnglishWords[strings.ToLower(text)]
		return transcriptNgram{folded: folded, ordinary: ordinary, start: i, end: i + 1, text: text, short: true}, true
	}
	if _, dup := seen[folded]; dup {
		return transcriptNgram{}, false
	}
	seen[folded] = struct{}{}
	ordinary := false
	if n == 1 {
		_, ordinary = commonEnglishWords[strings.ToLower(tokens[i])]
	}
	return transcriptNgram{folded: folded, ordinary: ordinary, start: i, end: i + n, text: text}, true
}

func foldPhonetic(s string) string {
	lowered := strings.ToLower(strings.TrimSpace(s))
	folded := dropSilentGH(stripToLetters(lowered))
	for _, rule := range phoneticRewrites {
		folded = strings.ReplaceAll(folded, rule.from, rule.to)
	}
	folded = softCBeforeFrontVowel(folded)
	folded = kuBeforeVowel(folded)
	return collapseFolded(folded)
}

func stripToLetters(lowered string) string {
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
	return stripped.String()
}

func collapseFolded(folded string) string {
	var out strings.Builder
	out.Grow(len(folded))
	var previous rune
	for i, r := range folded {
		if r == ' ' {
			continue
		}
		if r == previous {
			continue
		}
		if i > 0 && isFoldableVowel(r) {
			previous = r
			continue
		}
		out.WriteRune(r)
		previous = r
	}
	return out.String()
}

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
