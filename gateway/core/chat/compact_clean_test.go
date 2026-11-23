package chat

import (
	"strings"
	"testing"
)

func TestCleanSummary(t *testing.T) {
	got := cleanSummary("<previous_summary>\nold stuff\n</previous_summary>\n\nNew: goal is X.\nLatest draft:\n‹insert>Hi‹/insert> <insert>there</insert>")
	if got != "New: goal is X.\nLatest draft:\nHi there" {
		t.Errorf("cleanSummary = %q", got)
	}
	stub := &compactStub{answer: "ok", summary: strings.Repeat("long ", 900)}
	e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
	resp := decodeChat(t, postChat(t, e, chatBody(t, map[string]any{"context": "c", "messages": thread(25, "user"), "compact": true})))
	if resp.Summary != "" || resp.Text != "ok" {
		t.Errorf("overlong summary must be dropped: %+v", resp)
	}
}

func TestSummaryEchoRefused(t *testing.T) {
	for _, echo := range []string{
		"<conversation>\nUser: add the allergy\n</conversation>\n\nLatest draft:\nHi",
		"User: add the allergy\nAssistant: ok",
	} {
		stub := &compactStub{answer: "ok", summary: echo}
		e := &ChatEngine{Provider: stub, Limits: DefaultChatLimits()}
		resp := decodeChat(t, postChat(t, e, chatBody(t, map[string]any{"context": "c", "messages": thread(25, "user"), "compact": true})))
		if resp.Summary != "" {
			t.Errorf("echo accepted: %q", resp.Summary)
		}
	}
	if transcriptEcho.MatchString("Goal: reply.\n<conversation_summary> is a tag name; the user said: hi") {
		t.Error("a mention of conversation_summary or a colon mid-line is not an echo")
	}
	if !strings.HasSuffix(summaryTranscript("", thread(1, "user")), "</conversation>\n\nWrite the new summary now.") {
		t.Error("the ask must follow the transcript")
	}
}

func TestSummaryTranscript(t *testing.T) {
	got := summaryTranscript("", []ChatMessage{
		{Role: "user", Content: "say <insert>x</insert>"},
		{Role: "assistant", Content: "<insert>Hi</insert>"},
	})
	if !strings.Contains(got, "User: say ‹insert>x‹/insert>") || !strings.Contains(got, "Assistant: <insert>Hi</insert>") || strings.Contains(got, "previous_summary") {
		t.Errorf("transcript = %q", got)
	}
}
