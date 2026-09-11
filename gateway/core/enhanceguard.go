package core

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
)

// Deletion guard + decoder-loop repair for the Writing Tools pass.
//
// Lives in core, not beside one socket, because BOTH sockets that deliver an
// enhanced transcript must apply the same guard: the realtime socket
// (/v1/audio/stream/realtime) and the batch socket's split answer
// (/v1/audio/stream?split_enhance=true). Two copies would drift, and the drift
// would be silent — one path quietly shipping transcripts the other rejects.
//
// Named without the "live" prefix they carried when this was realtime-only.

// minWordKeepRatio is the floor on (enhanced words / raw words) before the
// enhanced result is rejected and the client keeps the raw transcript.
//
// Live output is fragment-heavy — WLK in particular emits short interjections
// ("Yeah. Yeah.") that a cleanup model is tempted to prune. Measured 2026-09-02
// (.claude/LIVE_AI_ENHANCEMENT_RESEARCH.md §7): normal filler removal on real Live
// text drops 3–7% of words, but one-shot cleanup of choppy WLK text deleted
// 12–15% — real content, not fillers — and the existing wordOverlapTooLow guard
// did not catch it. 0.75 sits between the two.
const minWordKeepRatio = 0.75

// EnhancedAcceptable is the deletion guard: it rejects an enhanced transcript that
// dropped too large a share of the raw words. Short transcripts are exempt — a five-word
// "um yeah okay so yeah" legitimately shrinks to almost nothing, and the ratio is
// meaningless at that length.
//
// The ratio is suspended entirely when the RAW transcript contains a decoder repetition
// loop (`Thank you. Thank you. Thank you…`). Collapsing such a run is the single most
// valuable thing the cleanup can do for the user, and it necessarily deletes most of the
// words — so the guard as originally written threw the good result away and handed back
// the hallucination. Judging the raw text rather than the size of the change is what
// separates "the model pruned real content" from "the model removed a loop". The
// realtime path has no other hallucination defence: `errSTTHallucination` is wired into
// the REST and batch-WS proxies only, never here.
func EnhancedAcceptable(raw, enhanced string) bool {
	if strings.TrimSpace(enhanced) == "" {
		return false
	}
	rawWords := len(strings.Fields(raw))
	if rawWords < 8 {
		return true
	}
	if TranscriptHasLoop(raw) {
		return true
	}
	return float64(len(strings.Fields(enhanced))) >= minWordKeepRatio*float64(rawWords)
}

const (
	// maxRepeatedPhraseWords is the longest phrase hasRepeatedPhrase looks for. Loops
	// longer than five words do occur but are rare, and every extra length costs a scan
	// of the whole transcript.
	maxRepeatedPhraseWords = 5

	// repeatedPhraseThreshold is how many back-to-back repeats of the same phrase mark a
	// decoder loop. Lower than core's single-word threshold of 10 because repeating a
	// whole phrase is far less natural than repeating one word: "no no no no no" is
	// speech, "thank you thank you thank you thank you thank you" is not.
	repeatedPhraseThreshold = 5

	// degenerateRunThreshold mirrors core's unexported degenerateRepetitionThreshold —
	// how many repeats of a single word count as a loop. Duplicated because the repair
	// below needs the number, not just core's yes/no verdict; keep the two in step.
	degenerateRunThreshold = 10
)

// hasRepeatedPhrase reports whether text contains the same multi-word phrase repeated
// back-to-back at least repeatedPhraseThreshold times.
//
// HasDegenerateRepetition only catches a single *word* looping, which misses the
// most common Whisper/WLK hallucination of all — a repeated phrase ("Thank you. Thank
// you. Thank you…"), where no individual word ever repeats consecutively because the
// phrase's own words alternate. Found 2026-09-02 when a real Live dictation came back as
// a phrase loop and the deletion guard rejected the cleanup that would have removed it.
func hasRepeatedPhrase(text string) bool {
	words := strings.Fields(text)
	for i, w := range words {
		words[i] = strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}))
	}
	// Shortest period first: "thank you" repeated 12 times also matches at n=4 (two
	// cycles) and n=6 (three), so scanning long-to-short would report the loop at the
	// wrong length. The minimal repeating unit is the honest one.
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

// equalWordRun reports whether two equal-length word slices match element for element.
func equalWordRun(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TranscriptHasLoop reports whether a Live transcript looks like a decoder
// repetition loop — either one word repeated (core's rule) or a whole phrase repeated
// (ours). One predicate so the telemetry, the deletion guard and the repair below all
// agree on what "hallucinated" means.
func TranscriptHasLoop(text string) bool {
	return HasDegenerateRepetition(text) || hasRepeatedPhrase(text)
}

// CollapseRepeatedRuns rewrites a transcript so each decoder repetition loop is reduced
// to a single occurrence, preserving the original words, casing and punctuation around
// it. Deterministic, no model involved.
//
// This is the Live path's repair of last resort: the batch proxies answer a hallucination
// by rejecting the transcript and retrying another backend, which Live cannot do — the
// audio is gone and the user is already looking at the text. So when the LLM cleanup that
// would normally fix the loop is unavailable or failed, this at least stops the loop
// reaching the document.
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
		collapsed := false
		// Shortest period first, for the same reason as hasRepeatedPhrase: a 12x "thank
		// you" loop also matches at n=4, and collapsing to that would leave two copies
		// behind instead of one. n=1 (a single word looping) is the shortest of all.
		for n := 1; n <= maxRepeatedPhraseWords; n++ {
			if i+n*2 > len(words) {
				continue
			}
			threshold := repeatedPhraseThreshold
			if n == 1 {
				threshold = degenerateRunThreshold
			}
			repeats := 1
			for next := i + n; next+n <= len(words); next += n {
				if !equalWordRun(norm[i:i+n], norm[next:next+n]) {
					break
				}
				repeats++
			}
			if repeats >= threshold {
				out = append(out, words[i:i+n]...)
				i += n * repeats
				collapsed = true
				break
			}
		}
		if !collapsed {
			out = append(out, words[i])
			i++
		}
	}
	return strings.Join(out, " ")
}

// ── Split enhance: raw final first, enhanced frame second ────────────────────

// EnhancedFrame is the post-delivery frame a client receives after its raw final,
// when it asked for `?split_enhance=true`. Exactly one is sent per session: either a
// text result, or status="failed" meaning "keep what you already have".
//
// Same JSON shape as the realtime socket's enhanced frame, deliberately — the iOS
// client decodes both with one parser.
type EnhancedFrame struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Status string `json:"status,omitempty"`
}

// splitEnhanceWriteTimeout bounds the write of the enhanced frame itself. The LLM
// wait is bounded separately, by the postProcess closure's own deadline.
const splitEnhanceWriteTimeout = 2 * time.Second

type splitEnhanceInput struct {
	raw         string
	contextJSON string
	intent      string
	enhance     func(context.Context, string, string, string) (string, string, error)
}

// postDeliveryOrInline picks the enhance budget for a split session.
//
// Once the raw final has been delivered the user is no longer blocked, so the
// post-delivery budget applies (ARCHITECTURE.md invariant 3). The community build
// wires a single closure and passes nil here, which keeps its existing behaviour.
func postDeliveryOrInline(
	inline, postDelivery func(context.Context, string, string, string) (string, string, error),
) func(context.Context, string, string, string) (string, string, error) {
	if postDelivery != nil {
		return postDelivery
	}
	return inline
}

// writeSplitEnhanceFrames writes the raw final immediately, then runs the Writing
// Tools pass and writes exactly one EnhancedFrame before closing.
//
// The contract the client depends on: after the raw frame, **exactly one** enhanced
// frame always follows — a result, or status="failed" on any error, rejection or
// timeout. A client that opted in must never be left waiting for a frame that cannot
// come, so every failure path below writes the failed frame rather than returning.
func (g *Gateway) writeSplitEnhanceFrames(ctx context.Context, conn *websocket.Conn, in splitEnhanceInput) {
	raw, _ := json.Marshal(streamResult{Text: in.raw})
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		log.Printf("ws write raw final: %v", err)
		if OnRequestFailed != nil {
			OnRequestFailed(ctx, errTypeSTTError)
		}
		return
	}

	frame := EnhancedFrame{Type: "enhanced", Status: "failed"}
	text, mode, err := in.enhance(ctx, in.raw, in.contextJSON, in.intent)
	switch {
	case err != nil:
		log.Printf("ws split enhance: %v", err)
		if OnError != nil {
			OnError(ctx, ErrorEvent{
				Source:     "stt",
				Kind:       "stt_post_process",
				Endpoint:   "/v1/audio/stream",
				InputChars: len(in.raw),
				Hint:       "split enhance failed; client keeps raw",
			})
		}
	case !EnhancedAcceptable(in.raw, text):
		// Deletion guard rejected it. The user already has the raw text on screen,
		// so "failed" here means "keep it" — never a silent truncation.
		log.Printf("ws split enhance: rejected, raw=%d chars enhanced=%d chars", len(in.raw), len(text))
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
