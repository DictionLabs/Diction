package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Long dictation and dictated questions on a self-hosted gateway. Measured 2026-10-11 against
// Groq gpt-oss (the QA box's model) with this build's request shape: a ~720-word Czech dictation
// came back cut off at 57-63% of its words with finish_reason=length (gpt-oss-120b) or empty
// (gpt-oss-20b), and the cut-off text was returned as the cleaned transcript. Record:
// diction-dev .claude/bow/explore-cleanup-cases.md.

func llmStub(t *testing.T, reply func(userMsg string) (content, finish string), gotMax *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages            []struct{ Role, Content string } `json:"messages"`
			MaxCompletionTokens int                              `json:"max_completion_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if gotMax != nil {
			*gotMax = req.MaxCompletionTokens
		}
		content, finish := reply(req.Messages[len(req.Messages)-1].Content)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}, "finish_reason": finish}},
		})
	}))
}

func longDictation(words int) string {
	w := strings.Fields("so the warehouse move happens in the second half of January and Marek talks to the landlord about the keys")
	out := make([]string, words)
	for i := range out {
		out[i] = w[i%len(w)]
	}
	return strings.Join(out, " ")
}

// A cleanup the model did not finish must never reach the user as if it were the transcript.
func TestLLM_TruncatedOutputIsAnError(t *testing.T) {
	srv := llmStub(t, func(u string) (string, string) {
		f := strings.Fields(u)
		return strings.Join(f[:len(f)*6/10], " "), "length"
	}, nil)
	defer srv.Close()
	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "m", Prompt: DefaultPromptCleanup}

	if out, err := cfg.processWithIntent(context.Background(), longDictation(700), "", ""); err == nil {
		t.Fatalf("finish_reason=length must be an error (caller keeps the raw transcript), got %d words", len(strings.Fields(out)))
	}
}

// A model that stops early on its own (finish_reason=stop) - a context window smaller than the
// dictation, or a model that condenses - is caught by the same deletion guard the split socket
// already applies (core.EnhancedAcceptable), so every cleanup path agrees.
func TestLLM_CleanupThatDropsMostWordsIsAnError(t *testing.T) {
	srv := llmStub(t, func(u string) (string, string) {
		f := strings.Fields(u)
		return strings.Join(f[:len(f)/2], " "), "stop"
	}, nil)
	defer srv.Close()
	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "m", Prompt: DefaultPromptCleanup}

	if _, err := cfg.processWithIntent(context.Background(), longDictation(700), "", ""); err == nil {
		t.Fatal("a cleanup that kept half the words must be an error")
	}
	// An ordinary cleanup that drops fillers still lands.
	srvOK := llmStub(t, func(u string) (string, string) { return u, "stop" }, nil)
	defer srvOK.Close()
	cfg.BaseURL = srvOK.URL
	if _, err := cfg.processWithIntent(context.Background(), longDictation(700), "", ""); err != nil {
		t.Fatalf("full-length cleanup must land: %v", err)
	}
}

// Reasoning models charge hidden thinking to the same budget, and the thinking grows with the
// input: gpt-oss-120b spent ~3,100 reasoning tokens on the Czech dictation above, so a budget
// of len/2+200 (floor 4000) ran out mid-answer. The budget keeps the output estimate AND a
// fixed reasoning allowance.
func TestLLM_LongInputBudgetKeepsReasoningHeadroom(t *testing.T) {
	var got int
	srv := llmStub(t, func(u string) (string, string) { return u, "stop" }, &got)
	defer srv.Close()
	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "m", Prompt: DefaultPromptCleanup}

	text := longDictation(700) // ~3,900 bytes, a ~5 minute dictation
	if _, err := cfg.processWithIntent(context.Background(), text, "", ""); err != nil {
		t.Fatal(err)
	}
	if want := len(text)/2 + 4000; got < want {
		t.Errorf("max_completion_tokens %d < %d (output estimate + reasoning allowance)", got, want)
	}
}

// The cleanup prompt must tell the model a dictated question or request is not addressed to it.
// Measured 2026-10-11 with the previous prompt: "can you help me write a short email to my
// landlord ..." was answered with a written email (gpt-oss-20b, 4/4 across default and low
// reasoning effort) or a refusal
// (gpt-oss-120b, 2/2), and the Czech equivalent with a written email on both (4/4).
func TestDefaultPromptCleanup_QuestionsAreNotAnswered(t *testing.T) {
	for _, want := range []string{"never addressed to you", "Never answer it", "still a question"} {
		if !strings.Contains(DefaultPromptCleanup, want) {
			t.Errorf("DefaultPromptCleanup lacks %q", want)
		}
	}
}
