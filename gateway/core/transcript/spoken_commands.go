package transcript

import (
	"strings"
	"unicode"
)

var spokenBreakPhrases = map[string]string{
	"new paragraph": "\n\n", "new line": "\n",
	"neuer absatz": "\n\n", "neue zeile": "\n", "nächste zeile": "\n",
	"nouveau paragraphe": "\n\n", "nouvelle ligne": "\n", "à la ligne": "\n",
	"nuevo párrafo": "\n\n", "punto y aparte": "\n\n", "nueva línea": "\n",
	"nuovo paragrafo": "\n\n", "nuova riga": "\n", "a capo": "\n",
	"nowy akapit": "\n\n", "nowa linia": "\n", "nowy wiersz": "\n",
	"nový odstavec": "\n\n", "nový řádek": "\n",
	"novo parágrafo": "\n\n", "nova linha": "\n",
	"nieuwe alinea": "\n\n", "nieuwe regel": "\n",
}

var spokenMarkPhrases = map[string]bool{
	"period": true, "full stop": true, "comma": true, "colon": true,
	"question mark": true, "exclamation mark": true, "exclamation point": true,
}

const maxSpokenPhraseWords = 3

func spokenWords(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	out := fields[:0]
	for _, f := range fields {
		w := strings.TrimFunc(f, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

func trailingPhrase(words []string, table func(string) bool) string {
	for n := maxSpokenPhraseWords; n >= 1; n-- {
		if len(words) < n {
			continue
		}
		p := strings.Join(words[len(words)-n:], " ")
		if table(p) {
			return p
		}
	}
	return ""
}

func leadingPhrase(words []string, table func(string) bool) string {
	for n := maxSpokenPhraseWords; n >= 1; n-- {
		if len(words) < n {
			continue
		}
		p := strings.Join(words[:n], " ")
		if table(p) {
			return p
		}
	}
	return ""
}

func isBreakPhrase(p string) bool { _, ok := spokenBreakPhrases[p]; return ok }

func SpokenTrailingBreak(transcript, cleaned string) string {
	p := trailingPhrase(spokenWords(transcript), isBreakPhrase)
	if p == "" {
		return ""
	}
	if trailingPhrase(spokenWords(cleaned), isBreakPhrase) == p {
		return ""
	}
	return spokenBreakPhrases[p]
}

func ApplySpokenTrailingBreak(transcript, cleaned, after string) string {
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	if strings.TrimSpace(cleaned) == "" {
		return cleaned
	}
	brk := SpokenTrailingBreak(transcript, cleaned)
	if brk == "" {
		return cleaned
	}
	have := len(after) - len(strings.TrimLeft(after, "\n"))
	if need := len(brk) - have; need > 0 {
		return cleaned + strings.Repeat("\n", need)
	}
	return cleaned
}

func SpokenLeadingBreak(transcript, cleaned string) string {
	p := leadingPhrase(spokenWords(transcript), isBreakPhrase)
	if p == "" {
		return ""
	}
	if leadingPhrase(spokenWords(cleaned), isBreakPhrase) == p {
		return ""
	}
	return spokenBreakPhrases[p]
}

func ApplySpokenLeadingBreak(transcript, cleaned, before string) string {
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	if strings.TrimSpace(cleaned) == "" {
		return cleaned
	}
	brk := SpokenLeadingBreak(transcript, cleaned)
	if brk == "" {
		return cleaned
	}
	cleaned = strings.TrimLeft(cleaned, "\n")
	before = strings.ReplaceAll(before, "\r\n", "\n")
	before = strings.TrimRight(before, " \t")
	have := len(before) - len(strings.TrimRight(before, "\n"))
	if need := len(brk) - have; need > 0 {
		return strings.Repeat("\n", need) + cleaned
	}
	return cleaned
}

func SpokenBreakOnly(transcript string) string {
	words := spokenWords(transcript)
	if len(words) == 0 {
		return ""
	}
	brk := ""
	for i := 0; i < len(words); {
		matched := 0
		for n := maxSpokenPhraseWords; n >= 1; n-- {
			if i+n <= len(words) && isBreakPhrase(strings.Join(words[i:i+n], " ")) {
				matched = n
				break
			}
		}
		if matched == 0 {
			return ""
		}
		brk += spokenBreakPhrases[strings.Join(words[i:i+matched], " ")]
		i += matched
	}
	if len(brk) > 2 {
		brk = "\n\n"
	}
	return brk
}

func ApplySpokenBreakOnly(transcript, cleaned, before, after string) (brk string, ok bool) {
	brk = SpokenBreakOnly(transcript)
	if brk == "" {
		return "", false
	}
	if strings.TrimSpace(cleaned) != "" && SpokenBreakOnly(cleaned) == "" {
		return "", false
	}
	before = strings.ReplaceAll(before, "\r\n", "\n")
	before = strings.TrimRight(before, " \t")
	after = strings.ReplaceAll(after, "\r\n", "\n")
	have := len(before) - len(strings.TrimRight(before, "\n"))
	have += len(after) - len(strings.TrimLeft(after, "\n"))
	if need := len(brk) - have; need > 0 {
		return strings.Repeat("\n", need), true
	}
	return "", true
}

func commandMask(words []string) []bool {
	mask := make([]bool, len(words))
	for i := 0; i < len(words); {
		matched := 0
		for n := maxSpokenPhraseWords; n >= 1; n-- {
			if i+n > len(words) {
				continue
			}
			p := strings.Join(words[i:i+n], " ")
			if isBreakPhrase(p) || spokenMarkPhrases[p] {
				matched = n
				break
			}
		}
		if matched == 0 {
			i++
			continue
		}
		for j := i; j < i+matched; j++ {
			mask[j] = true
		}
		i += matched
	}
	return mask
}

func SpokenCommandWordCount(raw string) int {
	count := 0
	for _, m := range commandMask(spokenWords(raw)) {
		if m {
			count++
		}
	}
	return count
}

var spokenNumberWords = map[string]bool{
	"zero": true, "oh": true, "one": true, "two": true, "three": true, "four": true,
	"five": true, "six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "thirteen": true, "fourteen": true, "fifteen": true,
	"sixteen": true, "seventeen": true, "eighteen": true, "nineteen": true, "twenty": true,
	"thirty": true, "forty": true, "fifty": true, "sixty": true, "seventy": true,
	"eighty": true, "ninety": true, "hundred": true, "thousand": true, "million": true,
}

func SpokenNumberWordDiscount(raw string) int {
	discount, run := 0, 0
	for _, w := range spokenWords(raw) {
		if spokenNumberWords[w] {
			run++
			continue
		}
		if run > 1 {
			discount += run - 1
		}
		run = 0
	}
	if run > 1 {
		discount += run - 1
	}
	return discount
}

func ContentWords(raw string) []string {
	words := spokenWords(raw)
	mask := commandMask(words)
	out := make([]string, 0, len(words))
	for i, w := range words {
		if !mask[i] && !spokenNumberWords[w] {
			out = append(out, w)
		}
	}
	return out
}
