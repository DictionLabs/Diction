package core

import (
	"errors"
	"strings"
	"unicode"
)

// errSTTHallucination signals that a backend transcript was rejected because
// it looked like a decoder repetition-loop hallucination, not because the
// backend request itself failed. Wrapping it lets both the WS and REST proxy
// paths reuse their existing backend-failure handling (retry / close /
// error-kind reporting) without inventing a parallel code path.
var errSTTHallucination = errors.New("stt: degenerate repetition detected")

// degenerateRepetitionThreshold is how many consecutive occurrences of the
// same normalized word we tolerate before treating the transcript as a
// hallucination. Real speech repeats short words ("no no no no no", ~5) but
// essentially never runs the same token 10+ times in a row — that pattern is
// a decoder repetition loop, typically triggered by marginal audio or a
// cold-loading model.
const degenerateRepetitionThreshold = 10

// hasDegenerateRepetition reports whether text contains a run of the same
// word repeated degenerateRepetitionThreshold times or more in a row.
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

// normalizeRepetitionWord lowercases a word and strips surrounding
// punctuation so "tamb," "Tamb" and "tamb." all collapse to the same token.
func normalizeRepetitionWord(w string) string {
	return strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}))
}
