package core

import (
	_ "embed"
	"sort"
	"strings"
	"sync"
)

// vocabWordsData is the 30,000 most frequent words of every shipped keyboard dictionary
// (ios/DictionKeyboard/Resources/LanguagePacks), lowercased, one per line, sorted. Built by
// .claude/research/mywords-prompt-eval/build_vocabwords.py.
//
// It answers "is this transcript word a real word or name of some language?", which is what
// separates another person's name from a garbled My Word. "Kristina" and Kristýna, "Andrej",
// "Ondra" and Ondřej are all in it; the STT's manglings of saved words ("Namotron", "Subabase",
// "Machila", "Zachidl") are not. See requireCorroboration rule 3. Every language is checked at
// once: names cross languages, and under auto-detect the gateway does not know the language.
//
//go:embed vocabwords.txt
var vocabWordsData string

var (
	vocabWordsOnce  sync.Once
	vocabWordsIndex []uint32 // byte offset of each line in vocabWordsData
)

// isDictionaryWord reports whether word, lowercased, is in vocabWordsData.
func isDictionaryWord(word string) bool {
	vocabWordsOnce.Do(func() {
		vocabWordsIndex = make([]uint32, 0, 270000)
		for start := 0; start < len(vocabWordsData); {
			vocabWordsIndex = append(vocabWordsIndex, uint32(start))
			end := strings.IndexByte(vocabWordsData[start:], '\n')
			if end < 0 {
				break
			}
			start += end + 1
		}
	})
	w := strings.ToLower(word)
	line := func(i int) string {
		s := vocabWordsData[vocabWordsIndex[i]:]
		if end := strings.IndexByte(s, '\n'); end >= 0 {
			s = s[:end]
		}
		return s
	}
	i := sort.Search(len(vocabWordsIndex), func(i int) bool { return line(i) >= w })
	return i < len(vocabWordsIndex) && line(i) == w
}
