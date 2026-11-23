package chat

import (
	"net/http"
	"strings"
	"testing"
)

func TestChatHandler_BackgroundCompactAnswersWithoutSummary(t *testing.T) {
	stub := &compactStub{answer: "<insert>ok</insert>", summary: "never"}
	e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
	var adm ChatAdmission
	e.Admit = func(_ *http.Request, a ChatAdmission) (func(ChatResult, error), *ChatRejection) {
		adm = a
		return func(ChatResult, error) {}, nil
	}
	for _, stream := range []bool{false, true} {
		rr := postChat(t, e, chatBody(t, map[string]any{
			"context": "c", "messages": thread(25, "user"), "insert_markup": true, "compact": true,
			"background_compact": true, "stream": stream,
		}))
		if rr.Code != http.StatusOK {
			t.Fatalf("stream=%v: %d %s", stream, rr.Code, rr.Body)
		}
		if len(stub.summaries) != 0 {
			t.Fatalf("stream=%v: no summary call inline, got %d", stream, len(stub.summaries))
		}
		if adm.Compact || adm.MaxTokens != 2048 {
			t.Errorf("stream=%v: admission is the plain chat's: %+v", stream, adm)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `"compact_due":true`) || strings.Contains(body, `"summary"`) {
			t.Errorf("stream=%v: body %s", stream, body)
		}
	}
	stub.answer = "ok"
	rr := postChat(t, e, chatBody(t, map[string]any{
		"context": "c", "messages": thread(3, "user"), "compact": true, "background_compact": true,
	}))
	if got := strings.TrimSpace(rr.Body.String()); got != `{"text":"ok","task":"chat","compact":true}` {
		t.Errorf("under the budget: %s", got)
	}
	rr = postChat(t, e, chatBody(t, map[string]any{"context": "c", "messages": thread(25, "user"), "background_compact": true}))
	if errCode(t, rr) != "too_many_turns" {
		t.Errorf("background_compact without compact: %d %s", rr.Code, rr.Body)
	}
}

func TestChatHandler_InlineCompactUnchangedWithoutBackgroundFlag(t *testing.T) {
	stub := &compactStub{answer: "a", summary: "s"}
	e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
	rr := postChat(t, e, chatBody(t, map[string]any{"context": "c", "messages": thread(25, "user"), "compact": true}))
	if got := strings.TrimSpace(rr.Body.String()); got != `{"text":"a","task":"chat","compact":true,"summary":"s","summarized_messages":19}` {
		t.Errorf("inline compaction body changed: %s", got)
	}
}

func TestChatHandler_CompactTask(t *testing.T) {
	stub := &compactStub{
		answer: "never", summary: "User drafts an email to Bob.",
		summaryRes: ChatResult{InputTokens: 2000, OutputTokens: 300},
	}
	e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
	var adm ChatAdmission
	var billed ChatResult
	admissions := 0
	e.Admit = func(_ *http.Request, a ChatAdmission) (func(ChatResult, error), *ChatRejection) {
		admissions++
		adm = a
		return func(res ChatResult, _ error) { billed = res }, nil
	}
	msgs := thread(26, "user")
	msgs[24].Content = "<insert>Hi Bob, draft one</insert>"
	msgs[25].Content = "<insert>Hi Bob, draft two</insert>"
	msgs[17].Content = "<insert>Hi Bob, draft zero</insert>"
	resp := decodeChat(t, postChat(t, e, chatBody(t, map[string]any{
		"task": "compact", "context": "MATERIAL-SECRET", "messages": msgs, "compact": true, "summary": "OLD",
		"stream": true,
	})))
	if len(stub.chats) != 0 || len(stub.summaries) != 1 {
		t.Fatalf("calls: %d chats %d summaries", len(stub.chats), len(stub.summaries))
	}
	in := stub.summaries[0].Messages[0].Content
	if strings.Contains(in, "MATERIAL-SECRET") || !strings.Contains(in, "<previous_summary>\nOLD") ||
		!strings.Contains(in, "message 19") || strings.Contains(in, "message 20") {
		t.Errorf("summariser input: %q", in)
	}
	if resp.Task != "compact" || !resp.Compact || resp.SummarizedMessages != 20 || resp.Text != "" {
		t.Errorf("resp %+v", resp)
	}
	if !strings.HasSuffix(resp.Summary, "Latest draft:\nHi Bob, draft zero") {
		t.Errorf("draft from the folded messages kept: %q", resp.Summary)
	}
	if admissions != 1 || adm.Task != "compact" || !adm.Compact || adm.MaxTokens != 1024 || adm.Stream {
		t.Errorf("admission %+v", adm)
	}
	if billed.InputTokens != 2000 || billed.OutputTokens != 300 {
		t.Errorf("billed %+v", billed)
	}

	stub.summaries = nil
	resp = decodeChat(t, postChat(t, e, chatBody(t, map[string]any{"task": "compact", "messages": thread(4, "user"), "compact": true})))
	if resp.Summary != "" || len(stub.summaries) != 0 || resp.Task != "compact" {
		t.Errorf("under budget: %+v, %d calls", resp, len(stub.summaries))
	}

	stub.summary = "User: hi\nAssistant: hello"
	rr := postChat(t, e, chatBody(t, map[string]any{"task": "compact", "messages": msgs, "compact": true}))
	if rr.Code != http.StatusBadGateway || errCode(t, rr) != "chat_failed" {
		t.Errorf("echoed summary: %d %s", rr.Code, rr.Body)
	}
}

func TestChatHandler_CompactTaskValidation(t *testing.T) {
	stub := &compactStub{summary: "s"}
	e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"needs compact", map[string]any{"task": "compact", "messages": thread(26, "user")}, "compact_required"},
		{"needs messages", map[string]any{"task": "compact", "compact": true}, "messages_required"},
		{"assistant first without summary", map[string]any{"task": "compact", "compact": true, "messages": thread(26, "assistant")}, "must_start_with_user"},
	}
	for _, c := range cases {
		rr := postChat(t, e, chatBody(t, c.body))
		if rr.Code != http.StatusBadRequest || errCode(t, rr) != c.code {
			t.Errorf("%s: %d %s", c.name, rr.Code, rr.Body)
		}
	}
	rr := postChat(t, e, chatBody(t, map[string]any{"task": "compact", "compact": true, "summary": "x", "messages": thread(25, "assistant")}))
	if rr.Code != http.StatusOK {
		t.Errorf("assistant first with summary: %d %s", rr.Code, rr.Body)
	}
}
