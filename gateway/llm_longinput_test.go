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

// The length guard is for the built-in prompt only: an operator's own LLM_PROMPT may
// summarise, condense or translate.
func TestLLM_LengthGuardOnlyForDefaultPrompt(t *testing.T) {
	short := func(u string) (string, string) {
		f := strings.Fields(u)
		return strings.Join(f[:len(f)/4], " "), "stop"
	}
	srv := llmStub(t, short, nil)
	defer srv.Close()
	for _, tc := range []struct {
		name    string
		prompt  string
		wantErr bool
	}{
		{"custom prompt keeps short output", "Summarise this.", false},
		{"default prompt rejects short output", DefaultPromptCleanup, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "m", Prompt: tc.prompt}
			_, err := cfg.processWithIntent(context.Background(), longDictation(200), "", "")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// A small-context server rejects the large budget with a 400; retry once with the old formula.
func TestLLM_ContextLimit400RetriesWithSmallerBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantCalls int
		wantErr   bool
	}{
		{"vllm max context", `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, 2, false},
		{"max_tokens", `{"error":"max_tokens is too large"}`, 2, false},
		{"context_length_exceeded", `{"error":{"code":"context_length_exceeded"}}`, 2, false},
		{"llama.cpp exceeds", `{"error":{"message":"the request exceeds the context size"}}`, 2, false},
		{"unrelated 400", `{"error":"invalid api key format"}`, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			var budgets []int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					MaxCompletionTokens int `json:"max_completion_tokens"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				calls++
				budgets = append(budgets, req.MaxCompletionTokens)
				if req.MaxCompletionTokens > 5000 {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(tc.body))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "finish_reason": "stop"}},
				})
			}))
			defer srv.Close()
			cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "m"}
			// 4000 chars: new budget 6000, old formula max(4000, 2200) = 4000.
			_, err := cfg.processWithPrompt(context.Background(), "p", strings.Repeat("x", 4000))
			if calls != tc.wantCalls || (err != nil) != tc.wantErr {
				t.Fatalf("calls=%d (want %d) err=%v budgets=%v", calls, tc.wantCalls, err, budgets)
			}
			if tc.wantCalls == 2 && budgets[1] != 4000 {
				t.Fatalf("retry budget %d, want 4000", budgets[1])
			}
		})
	}
}
