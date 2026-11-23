package chat

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const chatSummaryDraftLabel = "Latest draft:"

const openAISummaryMinTokens = 2048

func (l ChatLimits) summaryMaxTokens(provider string) int {
	if provider == providerOpenAI && l.SummaryMaxTokens < openAISummaryMinTokens {
		return openAISummaryMinTokens
	}
	return l.SummaryMaxTokens
}

func estimateChatTokens(s string) int {
	var t float64
	for _, r := range s {
		switch {
		case r < 0x80:
			t += 1.0 / 3
		case r < 0x2E80:
			t += 0.5
		default:
			t += 1.4
		}
	}
	return int(t + 0.5)
}

func (l ChatLimits) compactCut(msgs []ChatMessage, summary string) (cut int, ok bool) {
	tokens := estimateChatTokens(summary)
	for _, m := range msgs {
		tokens += estimateChatTokens(m.Content)
	}
	over := (l.CompactTokens > 0 && tokens >= l.CompactTokens) ||
		(l.CompactMessages > 0 && len(msgs) >= l.CompactMessages)
	if !over {
		return 0, false
	}
	cut = len(msgs) - max(l.KeepMessages, 1)
	if cut < 1 {
		return 0, false
	}
	return cut, true
}

func wrapChatSummary(summary string) string {
	if strings.TrimSpace(summary) == "" {
		return ""
	}
	return "<conversation_summary>\nThe earlier part of this conversation, summarised (those messages are no longer shown):\n" +
		neutraliseSummaryTags(summary) + "\n</conversation_summary>"
}

var summaryTag = regexp.MustCompile(`(?i)<(\s*/?\s*)(conversation_summary|previous_summary|conversation)\b`)

func neutraliseSummaryTags(s string) string {
	return summaryTag.ReplaceAllString(neutraliseMarkup(s), "‹${1}${2}")
}

func summaryTranscript(prev string, msgs []ChatMessage) string {
	var b strings.Builder
	if strings.TrimSpace(prev) != "" {
		b.WriteString("<previous_summary>\n")
		b.WriteString(neutraliseSummaryTags(strings.TrimSpace(prev)))
		b.WriteString("\n</previous_summary>\n\n")
	}
	b.WriteString("<conversation>\n")
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if m.Role == roleAssistant {
			b.WriteString("Assistant: ")
		} else {
			b.WriteString("User: ")
		}
		if m.Role == roleAssistant {
			b.WriteString(summaryTag.ReplaceAllString(strings.TrimSpace(m.Content), "‹${1}${2}"))
		} else {
			b.WriteString(neutraliseSummaryTags(strings.TrimSpace(m.Content)))
		}
	}
	b.WriteString("\n</conversation>\n\nWrite the new summary now.")
	return b.String()
}

var insertBlock = regexp.MustCompile(`(?s)<insert>(.*?)</insert>`)

func latestDraft(msgs []ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != roleAssistant {
			continue
		}
		if all := insertBlock.FindAllStringSubmatch(msgs[i].Content, -1); len(all) > 0 {
			return strings.TrimSpace(all[len(all)-1][1])
		}
	}
	return ""
}

func keepLatestDraft(summary string, msgs []ChatMessage) string {
	draft := latestDraft(msgs)
	if draft == "" || strings.Contains(strings.ToLower(summary), "latest draft") ||
		strings.Contains(squashSpace(summary), squashSpace(draft)) {
		return summary
	}
	return summary + "\n\n" + chatSummaryDraftLabel + "\n" + draft
}

func squashSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

var (
	errSummaryTruncated = errors.New("summary hit its token ceiling")
	errSummaryTooLong   = errors.New("summary longer than maxSummaryOutputRunes")
	errSummaryEcho      = errors.New("summary repeats the transcript")
)

const maxSummaryOutputRunes = 4000

var echoedPrevious = regexp.MustCompile(`(?s)^.*[<‹]\s*/\s*previous_summary\s*>`)

var transcriptEcho = regexp.MustCompile(`(?im)[<‹]\s*/?\s*conversation\b|^\s*(User|Assistant):`)

func cleanSummary(text string) string {
	text = echoedPrevious.ReplaceAllString(text, "")
	text = strings.NewReplacer(InsertOpenTag, "", InsertCloseTag, "", "‹insert>", "", "‹/insert>", "").Replace(text)
	return strings.TrimSpace(text)
}

type chatCompaction struct {
	cut  int
	call ChatCall
	msgs []ChatMessage
	done chan chatCompactionResult
}

type chatCompactionResult struct {
	summary string
	res     ChatResult
	err     error
}

func newChatCompaction(l ChatLimits, provider, prev string, msgs []ChatMessage, cut int) *chatCompaction {
	folded := msgs[:cut]
	return &chatCompaction{
		cut:  cut,
		msgs: folded,
		call: ChatCall{
			System:    ChatSummaryPrompt,
			Messages:  []ChatMessage{{Role: roleUser, Content: summaryTranscript(prev, folded)}},
			MaxTokens: l.summaryMaxTokens(provider),
		},
		done: make(chan chatCompactionResult, 1),
	}
}

func (c *chatCompaction) start(ctx context.Context, p ChatProvider) {
	go func() {
		res, err := p.Stream(ctx, c.call, func(string) {})
		out := chatCompactionResult{res: res, err: err}
		switch {
		case err != nil:
		case res.Refused:
			out.err = errChatRefused
		case res.Truncated:
			out.err = errSummaryTruncated
		default:
			s := cleanSummary(res.Text)
			switch {
			case s == "":
				out.err = errChatEmpty
			case transcriptEcho.MatchString(s):
				out.err = errSummaryEcho
			case utf8.RuneCountInString(s) > maxSummaryOutputRunes:
				out.err = errSummaryTooLong
			default:
				out.summary = keepLatestDraft(s, c.msgs)
			}
		}
		c.done <- out
	}()
}

func (c *chatCompaction) wait() chatCompactionResult { return <-c.done }

func addChatUsage(a, b ChatResult) ChatResult {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	return a
}
