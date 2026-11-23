package core

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/DictionLabs/Diction/gateway/core/transcript"

	"github.com/coder/websocket"
)

const minWordKeepRatio = 0.75

func EnhancedAcceptable(raw, enhanced string) bool {
	if strings.TrimSpace(enhanced) == "" {
		return false
	}
	rawWords := len(strings.Fields(raw)) - transcript.SpokenCommandWordCount(raw) - transcript.SpokenNumberWordDiscount(raw)
	if rawWords < 8 {
		return true
	}
	if TranscriptHasLoop(raw) {
		return true
	}
	return float64(len(strings.Fields(enhanced))) >= minWordKeepRatio*float64(rawWords)
}

const (
	maxRepeatedPhraseWords = 5

	repeatedPhraseThreshold = 5

	degenerateRunThreshold = 10
)

func hasRepeatedPhrase(text string) bool {
	words := strings.Fields(text)
	for i, w := range words {
		words[i] = strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}))
	}
	for n := 2; n <= maxRepeatedPhraseWords; n++ {
		if len(words) < n*repeatedPhraseThreshold {
			continue
		}
		for start := 0; start+n*repeatedPhraseThreshold <= len(words); start++ {
			repeats := 1
			for next := start + n; next+n <= len(words); next += n {
				if !equalWordRun(words[start:start+n], words[next:next+n]) {
					break
				}
				repeats++
				if repeats >= repeatedPhraseThreshold {
					return true
				}
			}
		}
	}
	return false
}

func equalWordRun(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TranscriptHasLoop(text string) bool {
	return transcript.HasDegenerateRepetition(text) || hasRepeatedPhrase(text)
}

func CollapseRepeatedRuns(text string) string {
	words := strings.Fields(text)
	norm := make([]string, len(words))
	for i, w := range words {
		norm[i] = strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}))
	}

	out := make([]string, 0, len(words))
	for i := 0; i < len(words); {
		if unit, repeats := repeatedRunAt(norm, i); unit > 0 {
			out = append(out, words[i:i+unit]...)
			i += unit * repeats
			continue
		}
		out = append(out, words[i])
		i++
	}
	return strings.Join(out, " ")
}

func repeatedRunAt(norm []string, i int) (unit, repeats int) {
	for n := 1; n <= maxRepeatedPhraseWords; n++ {
		if i+n*2 > len(norm) {
			continue
		}
		threshold := repeatedPhraseThreshold
		if n == 1 {
			threshold = degenerateRunThreshold
		}
		if r := countRunRepeats(norm, i, n); r >= threshold {
			return n, r
		}
	}
	return 0, 0
}

func countRunRepeats(norm []string, i, n int) int {
	repeats := 1
	for next := i + n; next+n <= len(norm); next += n {
		if !equalWordRun(norm[i:i+n], norm[next:next+n]) {
			break
		}
		repeats++
	}
	return repeats
}

type EnhancedFrame struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Status string `json:"status,omitempty"`
}

const splitEnhanceWriteTimeout = 2 * time.Second

type splitEnhanceInput struct {
	raw         string
	contextJSON string
	intent      string
	enhance     func(context.Context, string, string, string) (string, string, error)
}

func postDeliveryOrInline(
	inline, postDelivery func(context.Context, string, string, string) (string, string, error),
) func(context.Context, string, string, string) (string, string, error) {
	if postDelivery != nil {
		return postDelivery
	}
	return inline
}

func (g *Gateway) writeSplitEnhanceFrames(ctx context.Context, conn *websocket.Conn, in splitEnhanceInput) {
	raw, _ := json.Marshal(streamResult{Text: in.raw})
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		log.Printf("ws write raw final: %v", err)
		if OnRequestFailed != nil {
			OnRequestFailed(ctx, errTypeSTTError)
		}
		return
	}

	frame := EnhancedFrame{Type: "enhanced", Status: statusFailed}
	text, mode, err := in.enhance(ctx, in.raw, in.contextJSON, in.intent)
	switch {
	case err != nil:
		log.Printf("ws split enhance: %v", err)
		ReportEnhanceSkipped(ctx, EnhanceSkipReason(ctx, err))
	case !EnhancedAcceptable(in.raw, text):
		log.Printf("ws split enhance: rejected, raw=%d chars enhanced=%d chars", len(in.raw), len(text))
		ReportEnhanceSkipped(ctx, EnhanceSkipRejected)
	default:
		frame = EnhancedFrame{Type: "enhanced", Text: text, Mode: mode}
	}

	writeCtx, cancel := context.WithTimeout(ctx, splitEnhanceWriteTimeout)
	defer cancel()
	if payload, mErr := json.Marshal(frame); mErr != nil {
		log.Printf("ws split enhance: marshal frame: %v", mErr)
	} else if wErr := conn.Write(writeCtx, websocket.MessageText, payload); wErr != nil {
		log.Printf("ws split enhance: write frame: %v", wErr)
	}
	CloseWSWithTimeout(conn, websocket.StatusNormalClosure, "", 2*time.Second)
}
