package transcript

import (
	"errors"
	"strings"
	"unicode"
)

var ErrSTTHallucination = errors.New("stt: degenerate repetition detected")

var ErrImplausibleLength error = implausibleLengthError{}

type implausibleLengthError struct{}

func (implausibleLengthError) Error() string        { return "stt: more text than the audio can hold" }
func (implausibleLengthError) Is(target error) bool { return target == ErrSTTHallucination }

const degenerateRepetitionThreshold = 10

func hasDegenerateRepetition(text string) bool {
	words := strings.Fields(text)
	if len(words) < degenerateRepetitionThreshold {
		return false
	}

	run := 1
	prev := normalizeRepetitionWord(words[0])
	for _, w := range words[1:] {
		cur := normalizeRepetitionWord(w)
		if cur != "" && cur == prev {
			run++
			if run >= degenerateRepetitionThreshold {
				return true
			}
		} else {
			run = 1
		}
		prev = cur
	}
	return false
}

func HasDegenerateRepetition(text string) bool {
	return hasDegenerateRepetition(text)
}

func normalizeRepetitionWord(w string) string {
	return strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}))
}
