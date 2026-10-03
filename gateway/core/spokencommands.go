package core

import (
	"strings"
	"unicode"
)

// Spoken formatting commands: "new paragraph" / "new line" dictated inside ordinary
// dictation. The cleanup LLM turns them into breaks in the middle of the text (the SPOKEN
// FORMATTING COMMANDS block in prompts.CleanupSystemPrompt). Two things it cannot do, which
// live here instead:
//
//   - A break at the END of a dictation. Tokenizers normalise edge whitespace and the gateway
//     TrimSpaces the model's answer anyway, so "see you tomorrow new paragraph" always came
//     back as "See you tomorrow." ApplySpokenTrailingBreak restores it from the transcript.
//   - The deletion guard. Every command removes words, so a correct cleanup of a
//     command-dense dictation fell under EnhancedAcceptable's keep ratio.
//     SpokenCommandWordCount lets the guard discount them.
//
// The phrase table is language-bounded on purpose: an unknown-language trailing command just
// loses its break, as before. "next paragraph" / "next line" are deliberately absent — they
// end ordinary sentences ("see the next paragraph"). See
// .claude/plans/spoken-formatting-commands-plan.md.

// spokenBreakPhrases maps a lowercase command phrase to the break it stands for.
var spokenBreakPhrases = map[string]string{
	// en
	"new paragraph": "\n\n", "new line": "\n",
	// de
	"neuer absatz": "\n\n", "neue zeile": "\n", "nächste zeile": "\n",
	// fr
	"nouveau paragraphe": "\n\n", "nouvelle ligne": "\n", "à la ligne": "\n",
	// es
	"nuevo párrafo": "\n\n", "punto y aparte": "\n\n", "nueva línea": "\n",
	// it
	"nuovo paragrafo": "\n\n", "nuova riga": "\n", "a capo": "\n",
	// pl
	"nowy akapit": "\n\n", "nowa linia": "\n", "nowy wiersz": "\n",
	// cs
	"nový odstavec": "\n\n", "nový řádek": "\n",
	// pt
	"novo parágrafo": "\n\n", "nova linha": "\n",
	// nl
	"nieuwe alinea": "\n\n", "nieuwe regel": "\n",
}

// spokenMarkPhrases are the English punctuation names the cleanup turns into marks. Used only
// to discount words in the deletion guard, never to insert anything.
var spokenMarkPhrases = map[string]bool{
	"period": true, "full stop": true, "comma": true, "colon": true,
	"question mark": true, "exclamation mark": true, "exclamation point": true,
}

// maxSpokenPhraseWords is the longest phrase in either table ("punto y aparte").
const maxSpokenPhraseWords = 3

// spokenWords splits text into lowercase words with edge punctuation stripped, dropping
// tokens that were punctuation only.
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

// trailingPhrase returns the command phrase the words end with, longest first, or "".
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

func isBreakPhrase(p string) bool { _, ok := spokenBreakPhrases[p]; return ok }

// SpokenTrailingBreak returns the break a transcript's final command stands for, or "" when
// the transcript does not end with one or the cleaned text still ends with the phrase (the
// model judged it to be content: "…I added a new paragraph").
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

// ApplySpokenTrailingBreak normalises line breaks in cleaned to "\n" and appends the break a
// trailing spoken command asked for, minus the newlines the text after the cursor already
// starts with, so a join never exceeds one blank line. Identity otherwise.
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

// commandMask marks which of words belong to a spoken formatting command phrase (break
// phrase in any listed language, or an English mark name), matching longest phrases first.
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

// SpokenCommandWordCount counts the words of a raw transcript that belong to a spoken
// formatting command, anywhere in it. The deletion guard discounts them: the cleanup is
// supposed to remove them. Literal uses ("a new line of shoes") are counted too — the guard
// loosens by at most the phrase length for each, which is accepted.
func SpokenCommandWordCount(raw string) int {
	count := 0
	for _, m := range commandMask(spokenWords(raw)) {
		if m {
			count++
		}
	}
	return count
}

// spokenNumberWords are the English number words the cleanup turns into digits. English
// only, like spokenMarkPhrases — the STT models this matters for write digits themselves in
// most other languages. "oh" is here for digit strings ("three oh three").
var spokenNumberWords = map[string]bool{
	"zero": true, "oh": true, "one": true, "two": true, "three": true, "four": true,
	"five": true, "six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "thirteen": true, "fourteen": true, "fifteen": true,
	"sixteen": true, "seventeen": true, "eighteen": true, "nineteen": true, "twenty": true,
	"thirty": true, "forty": true, "fifty": true, "sixty": true, "seventy": true,
	"eighty": true, "ninety": true, "hundred": true, "thousand": true, "million": true,
}

// SpokenNumberWordDiscount is how many words a raw transcript loses when the cleanup writes
// its spelled-out numbers as digits: each run of number words ("seventy six twenty") can
// collapse into one token ("7620"), so every word of a run but one is discounted. A lone
// number word discounts nothing.
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

// ContentWords returns the words of a raw transcript a correct cleanup is expected to keep:
// everything except spoken command phrases (removed) and number words (rewritten as digits).
// Lowercase, edge punctuation stripped. The word-overlap guard measures against these.
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
