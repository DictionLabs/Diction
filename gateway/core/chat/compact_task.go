package chat

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (l ChatLimits) validateCompactTask(req *chatRequest) *chatError {
	if !req.Compact {
		return &chatError{status: http.StatusBadRequest, code: "compact_required"}
	}
	req.Context = ""
	req.Stream = false
	req.Summary = shortenRunes(strings.TrimSpace(req.Summary), l.MaxSummaryChars)
	if len(req.Messages) == 0 {
		return &chatError{status: http.StatusBadRequest, code: "messages_required"}
	}
	total := 0
	for i, m := range req.Messages {
		if m.Role != roleUser && m.Role != roleAssistant {
			return &chatError{status: http.StatusBadRequest, code: "invalid_role"}
		}
		if strings.TrimSpace(m.Content) == "" {
			return &chatError{status: http.StatusBadRequest, code: "empty_message"}
		}
		if i > 0 && req.Messages[i-1].Role == m.Role {
			return &chatError{status: http.StatusBadRequest, code: "roles_must_alternate"}
		}
		n := utf8.RuneCountInString(m.Content)
		if n > l.MaxMessageChars {
			return &chatError{
				status: http.StatusRequestEntityTooLarge,
				code:   "message_too_long",
				detail: map[string]any{keyMaxChars: l.MaxMessageChars},
			}
		}
		total += n
	}
	if req.Messages[0].Role != roleUser && req.Summary == "" {
		return &chatError{status: http.StatusBadRequest, code: "must_start_with_user"}
	}
	if total > l.CompactMaxTotalChars {
		return &chatError{
			status: http.StatusRequestEntityTooLarge,
			code:   "conversation_too_long",
			detail: map[string]any{keyMaxChars: l.CompactMaxTotalChars},
		}
	}
	return nil
}

func serveCompactTask(w http.ResponseWriter, r *http.Request, e *ChatEngine, req *chatRequest) {
	lim := e.Limits
	resp := chatResponse{Task: taskCompact, Compact: true}
	cut, ok := lim.compactCut(req.Messages, req.Summary)
	if !ok {
		writeChatJSON(w, resp)
		return
	}
	c := newChatCompaction(lim, e.Provider.Name(), req.Summary, req.Messages, cut)
	var out chatCompactionResult
	if e.Admit != nil {
		done, rej := e.Admit(r, ChatAdmission{
			Task: taskCompact, Provider: e.Provider.Name(), Compact: true,
			MaxTokens: c.call.MaxTokens, InputBytes: chatCallInputBytes(c.call),
		})
		if rej != nil {
			if rej.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(rej.RetryAfter))
			}
			writeChatError(w, rej.Status, rej.Code, rej.Detail)
			return
		}
		defer func() { done(out.res, out.err) }()
	}
	ctx, cancel := context.WithTimeout(r.Context(), lim.Timeout)
	defer cancel()
	start := time.Now()
	c.start(ctx, e.Provider)
	out = c.wait()
	outcome := ChatOutcome{
		Task:     taskCompact,
		Provider: e.Provider.Name(),
		Turns:    countUserTurns(req.Messages),
		Result:   out.res,
		Elapsed:  time.Since(start),
		Err:      out.err,
	}
	for _, m := range req.Messages {
		outcome.MessageChars += utf8.RuneCountInString(m.Content)
	}
	if e.OnResult != nil {
		e.OnResult(r.Context(), outcome)
	}
	log.Printf("chat: task=compact provider=%s model=%s folded=%d kept=%d summary_chars=%d in=%d cache_read=%d out=%d ms=%d err=%v",
		outcome.Provider, out.res.Model, cut, len(req.Messages)-cut, utf8.RuneCountInString(out.summary),
		out.res.InputTokens+out.res.CacheWriteTokens, out.res.CacheReadTokens, out.res.OutputTokens, outcome.Elapsed.Milliseconds(), out.err)
	if out.err != nil {
		writeChatError(w, http.StatusBadGateway, "chat_failed", map[string]any{keyReason: chatFailureReason(out.err)})
		return
	}
	resp.Summary, resp.SummarizedMessages = out.summary, cut
	writeChatJSON(w, resp)
}

func writeChatJSON(w http.ResponseWriter, resp chatResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
