package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type chatRequest struct {
	Task              string        `json:"task"`
	Context           string        `json:"context"`
	Messages          []ChatMessage `json:"messages"`
	Stream            bool          `json:"stream"`
	InsertMarkup      bool          `json:"insert_markup"`
	Compact           bool          `json:"compact"`
	Summary           string        `json:"summary"`
	BackgroundCompact bool          `json:"background_compact"`
}

type chatResponse struct {
	Text               string `json:"text"`
	Task               string `json:"task"`
	Truncated          bool   `json:"truncated,omitempty"`
	InsertMarkup       bool   `json:"insert_markup,omitempty"`
	Compact            bool   `json:"compact,omitempty"`
	Summary            string `json:"summary,omitempty"`
	SummarizedMessages int    `json:"summarized_messages,omitempty"`
	CompactDue         bool   `json:"compact_due,omitempty"`
}

type chatError struct {
	status int
	code   string
	detail map[string]any
}

func (e *chatError) Error() string { return e.code }

func (l ChatLimits) validate(req *chatRequest) *chatError {
	switch req.Task {
	case "", taskChat:
		req.Task = taskChat
	case taskTitle:
	case taskCompact:
		return l.validateCompactTask(req)
	default:
		return &chatError{status: http.StatusBadRequest, code: "unknown_task"}
	}
	req.Context = shortenRunes(req.Context, l.MaxContextChars)
	compact := req.Compact && req.Task == taskChat
	if compact {
		req.Summary = shortenRunes(strings.TrimSpace(req.Summary), l.MaxSummaryChars)
	} else {
		req.Compact, req.Summary, req.BackgroundCompact = false, "", false
	}
	if req.Task == taskTitle {
		if strings.TrimSpace(req.Context) == "" {
			return &chatError{status: http.StatusBadRequest, code: "context_required"}
		}
		return nil
	}
	if len(req.Messages) == 0 {
		return &chatError{status: http.StatusBadRequest, code: "messages_required"}
	}
	turns, total, cerr := l.checkMessages(req.Messages)
	if cerr != nil {
		return cerr
	}
	return l.checkShape(req, compact, turns, total)
}

func (l ChatLimits) checkShape(req *chatRequest, compact bool, turns, total int) *chatError {
	startsOK := req.Messages[0].Role == roleUser || req.Summary != ""
	if !startsOK || req.Messages[len(req.Messages)-1].Role != roleUser {
		return &chatError{status: http.StatusBadRequest, code: "must_start_and_end_with_user"}
	}
	if !compact && turns > l.MaxTurns {
		return &chatError{status: http.StatusRequestEntityTooLarge, code: "too_many_turns", detail: map[string]any{"max_turns": l.MaxTurns}}
	}
	maxTotal := l.MaxTotalChars
	if compact {
		maxTotal = l.CompactMaxTotalChars
	}
	if total > maxTotal {
		return &chatError{status: http.StatusRequestEntityTooLarge, code: "conversation_too_long", detail: map[string]any{keyMaxChars: maxTotal}}
	}
	return nil
}

func (l ChatLimits) checkMessages(msgs []ChatMessage) (turns, total int, cerr *chatError) {
	for i, m := range msgs {
		switch m.Role {
		case roleUser:
			turns++
		case roleAssistant:
		default:
			return 0, 0, &chatError{status: http.StatusBadRequest, code: "invalid_role"}
		}
		if strings.TrimSpace(m.Content) == "" {
			return 0, 0, &chatError{status: http.StatusBadRequest, code: "empty_message"}
		}
		if i > 0 && msgs[i-1].Role == m.Role {
			return 0, 0, &chatError{status: http.StatusBadRequest, code: "roles_must_alternate"}
		}
		n := utf8.RuneCountInString(m.Content)
		if n > l.MaxMessageChars {
			return 0, 0, &chatError{
				status: http.StatusRequestEntityTooLarge,
				code:   "message_too_long",
				detail: map[string]any{keyMaxChars: l.MaxMessageChars},
			}
		}
		total += n
	}
	return turns, total, nil
}

func shortenRunes(s string, limit int) string {
	if limit < 1 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit-1]) + "…"
}

func writeChatError(w http.ResponseWriter, status int, code string, detail map[string]any) {
	body := map[string]any{"error": code}
	for k, v := range detail {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.TrimPrefix(s, "Title:")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'“”„«»`*")
	s = strings.TrimRight(s, ".。")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 80 {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:80]))
	}
	return s
}

func ChatHandler(e *ChatEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := readChatRequest(w, r, e)
		if !ok {
			return
		}
		if req.Task == taskCompact {
			serveCompactTask(w, r, e, req)
			return
		}
		runChat(w, r, e, req)
	}
}

func readChatRequest(w http.ResponseWriter, r *http.Request, e *ChatEngine) (*chatRequest, bool) {
	if r.Method != http.MethodPost {
		writeChatError(w, http.StatusMethodNotAllowed, "method_not_allowed", nil)
		return nil, false
	}
	if !e.Enabled() {
		writeChatError(w, http.StatusServiceUnavailable, "chat_not_configured", nil)
		return nil, false
	}
	if r.Header.Get("X-Diction-E2E") != "" {
		writeChatError(w, http.StatusBadRequest, "e2e_unsupported", nil)
		return nil, false
	}
	lim := e.Limits
	r.Body = http.MaxBytesReader(w, r.Body, lim.MaxBodyBytes)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeChatError(w, http.StatusRequestEntityTooLarge, "body_too_large", nil)
			return nil, false
		}
		writeChatError(w, http.StatusBadRequest, "invalid_body", nil)
		return nil, false
	}
	if cerr := lim.validate(&req); cerr != nil {
		writeChatError(w, cerr.status, cerr.code, cerr.detail)
		return nil, false
	}
	return &req, true
}

type chatState struct {
	res    ChatResult
	billed ChatResult
	err    error
}

func buildChatCall(e *ChatEngine, req *chatRequest) (call ChatCall, stream, markup bool) {
	lim := e.Limits
	markup = req.InsertMarkup && req.Task == taskChat
	call = ChatCall{
		System:    e.system(markup),
		Context:   wrapChatContext(req.Context),
		Summary:   wrapChatSummary(req.Summary),
		Messages:  req.Messages,
		MaxTokens: lim.MaxOutputTokens,
	}
	if markup {
		call.Messages = neutraliseUserMessages(req.Messages)
	}
	stream = req.Stream
	if req.Task == taskTitle {
		call = ChatCall{
			System:    ChatTitlePrompt,
			Context:   wrapChatContext(req.Context),
			Messages:  []ChatMessage{{Role: roleUser, Content: "Title for the shared text."}},
			MaxTokens: lim.titleMaxTokens(e.Provider.Name()),
		}
		stream = false
	}
	return call, stream, markup
}

func planCompaction(e *ChatEngine, req *chatRequest) (compaction *chatCompaction, compactDue bool) {
	if !req.Compact {
		return nil, false
	}
	cut, ok := e.Limits.compactCut(req.Messages, req.Summary)
	if !ok {
		return nil, false
	}
	if req.BackgroundCompact {
		return nil, true
	}
	return newChatCompaction(e.Limits, e.Provider.Name(), req.Summary, req.Messages, cut), false
}

func admitChat(
	w http.ResponseWriter, r *http.Request, e *ChatEngine, req *chatRequest,
	stream bool, call ChatCall, compaction *chatCompaction,
) (admitted func(ChatResult, error), ok bool) {
	if e.Admit == nil {
		return nil, true
	}
	adm := ChatAdmission{
		Task: req.Task, Stream: stream, Provider: e.Provider.Name(), MaxTokens: call.MaxTokens,
		InputBytes: chatCallInputBytes(call),
	}
	if compaction != nil {
		adm.Compact = true
		adm.MaxTokens += compaction.call.MaxTokens
		adm.InputBytes += chatCallInputBytes(compaction.call)
	}
	done, rej := e.Admit(r, adm)
	if rej != nil {
		if rej.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(rej.RetryAfter))
		}
		writeChatError(w, rej.Status, rej.Code, rej.Detail)
		return nil, false
	}
	return done, true
}

func newChatOutcome(e *ChatEngine, req *chatRequest, stream bool) ChatOutcome {
	outcome := ChatOutcome{
		Task:         req.Task,
		Provider:     e.Provider.Name(),
		Stream:       stream,
		ContextChars: utf8.RuneCountInString(req.Context),
		Turns:        countUserTurns(req.Messages),
	}
	if req.Task == taskChat {
		for _, m := range req.Messages {
			outcome.MessageChars += utf8.RuneCountInString(m.Content)
		}
	}
	return outcome
}

func openChatStream(w http.ResponseWriter, stream bool) (onDelta func(string), flush func()) {
	if !stream {
		return func(string) {}, nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	flush = func() { _ = rc.Flush() }
	flush()
	onDelta = func(piece string) {
		writeSSE(w, "delta", map[string]string{"text": piece})
		flush()
	}
	return onDelta, flush
}

func (st *chatState) foldCompaction(
	compaction *chatCompaction, cancel context.CancelFunc, msgs []ChatMessage,
) chatCompactionResult {
	var summary chatCompactionResult
	if compaction == nil {
		return summary
	}
	if st.err != nil {
		cancel()
	}
	summary = compaction.wait()
	st.billed = addChatUsage(st.res, summary.res)
	log.Printf("chat: compact folded=%d kept=%d summary_chars=%d in=%d cache_read=%d out=%d err=%v",
		compaction.cut, len(msgs)-compaction.cut, utf8.RuneCountInString(summary.summary),
		summary.res.InputTokens+summary.res.CacheWriteTokens, summary.res.CacheReadTokens, summary.res.OutputTokens, summary.err)
	return summary
}

func logChatOutcome(outcome ChatOutcome, markup, stream bool, res ChatResult, err error) {
	log.Printf("chat: task=%s markup=%v provider=%s model=%s stream=%v ctx_chars=%d turns=%d in=%d cache_read=%d cache_write=%d out=%d ttft_ms=%d ms=%d stop=%s err=%v",
		outcome.Task, markup, outcome.Provider, res.Model, stream, outcome.ContextChars, outcome.Turns,
		res.InputTokens, res.CacheReadTokens, res.CacheWriteTokens, res.OutputTokens,
		res.TTFT.Milliseconds(), outcome.Elapsed.Milliseconds(), res.StopReason, err)
}

func runChat(w http.ResponseWriter, r *http.Request, e *ChatEngine, req *chatRequest) {
	lim := e.Limits
	call, stream, markup := buildChatCall(e, req)
	compaction, compactDue := planCompaction(e, req)
	admitted, ok := admitChat(w, r, e, req, stream, call, compaction)
	if !ok {
		return
	}
	st := &chatState{}
	if admitted != nil {
		defer func() { admitted(st.billed, st.err) }()
	}

	ctx, cancel := context.WithTimeout(r.Context(), lim.Timeout)
	defer cancel()
	if compaction != nil {
		compaction.start(ctx, e.Provider)
	}
	start := time.Now()
	outcome := newChatOutcome(e, req, stream)
	onDelta, flush := openChatStream(w, stream)

	st.res, st.err = e.Provider.Stream(ctx, call, onDelta)
	st.billed = st.res
	if st.err == nil && st.res.Refused {
		st.err = errChatRefused
	}
	if st.err == nil && strings.TrimSpace(st.res.Text) == "" {
		st.err = errChatEmpty
	}
	summary := st.foldCompaction(compaction, cancel, req.Messages)
	res, err := st.res, st.err
	outcome.Elapsed = time.Since(start)
	outcome.Result, outcome.Err = res, err
	if e.OnResult != nil {
		e.OnResult(r.Context(), outcome)
	}
	logChatOutcome(outcome, markup, stream, res, err)

	reason := chatFailureReason(err)
	resp := chatResponse{
		Text:         strings.TrimSpace(res.Text),
		Task:         req.Task,
		Truncated:    res.Truncated,
		InsertMarkup: markup,
		Compact:      req.Compact,
		CompactDue:   compactDue,
	}
	if summary.err == nil && summary.summary != "" {
		resp.Summary, resp.SummarizedMessages = summary.summary, compaction.cut
	}
	if stream {
		if err != nil {
			writeSSE(w, "error", map[string]string{"error": "chat_failed", keyReason: reason})
		} else {
			writeSSE(w, "done", resp)
		}
		flush()
		return
	}
	if err != nil {
		writeChatError(w, http.StatusBadGateway, "chat_failed", map[string]any{keyReason: reason})
		return
	}
	if req.Task == taskTitle {
		resp.Text = cleanTitle(resp.Text)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

var (
	errChatRefused = errors.New("provider declined the request")
	errChatEmpty   = errors.New("provider returned no text")
)

func chatFailureReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errChatRefused):
		return "refused"
	case errors.Is(err, errChatEmpty):
		return "empty"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "provider_error"
	}
}

func chatCallInputBytes(c ChatCall) int {
	n := len(c.System) + len(c.Context) + len(c.Summary)
	for _, m := range c.Messages {
		n += len(m.Content)
	}
	return n
}

func countUserTurns(msgs []ChatMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Role == roleUser {
			n++
		}
	}
	return n
}

func writeSSE(w http.ResponseWriter, event string, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
