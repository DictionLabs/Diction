package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeOpenAI struct {
	mu      sync.Mutex
	bodies  []map[string]any
	auth    []string
	pieces  []string
	finish  string
	status  int
	jsonish bool
}

func (f *fakeOpenAI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("openai path = %s, want /v1/chat/completions", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		finish := f.finish
		if finish == "" {
			finish = "stop"
		}
		if f.jsonish {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"model":"m","choices":[{"message":{"content":%q},"finish_reason":%q}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`, strings.Join(f.pieces, ""), finish)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, p := range f.pieces {
			b, _ := json.Marshal(p)
			fmt.Fprintf(w, "data: {\"model\":\"m-1\",\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n", b)
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":60}}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}
}

func TestOpenAIChat_StreamsTextAndUsage(t *testing.T) {
	fake := &fakeOpenAI{pieces: []string{"I'm in", ", running late"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1/", APIKey: "sk-x", Model: "gpt-x", ReasoningEffort: "low"})

	var deltas []string
	res, err := p.Stream(context.Background(), testCall(), func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if res.Text != "I'm in, running late" || len(deltas) != 2 || res.Model != "m-1" {
		t.Errorf("text=%q deltas=%v model=%q", res.Text, deltas, res.Model)
	}
	if res.InputTokens != 40 || res.CacheReadTokens != 60 || res.OutputTokens != 7 || res.StopReason != "stop" {
		t.Errorf("usage in=%d cached=%d out=%d stop=%q", res.InputTokens, res.CacheReadTokens, res.OutputTokens, res.StopReason)
	}
	if fake.auth[0] != "Bearer sk-x" {
		t.Errorf("auth = %q", fake.auth[0])
	}
	b := fake.bodies[0]
	if b["model"] != "gpt-x" || b["stream"] != true || b["max_completion_tokens"].(float64) != 256 || b["reasoning_effort"] != "low" {
		t.Errorf("body = %v", b)
	}
	if so, _ := b["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", b["stream_options"])
	}
	msgs := b["messages"].([]any)
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != ChatSystemPrompt {
		t.Errorf("system message must be the instructions only: %v", sys)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	user := msgs[1].(map[string]any)
	content, _ := user["content"].(string)
	if user["role"] != "user" || !strings.HasPrefix(content, "<shared_context>\nAnna") ||
		!strings.HasSuffix(content, "</shared_context>\n\nreply, say I'm in but late") {
		t.Errorf("first user message must open with the shared text: %v", user)
	}
}

func TestOpenAIChat_LocalServerNoKeyNoEffort(t *testing.T) {
	fake := &fakeOpenAI{pieces: []string{"hi"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1", Model: "llama3"})
	if _, err := p.Stream(context.Background(), testCall(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if fake.auth[0] != "" {
		t.Errorf("no key must send no Authorization, got %q", fake.auth[0])
	}
	if _, has := fake.bodies[0]["reasoning_effort"]; has {
		t.Error("empty ReasoningEffort must not be sent (Ollama/LM Studio reject unknown values)")
	}
}

func TestOpenAIChat_NonStreamingServer(t *testing.T) {
	fake := &fakeOpenAI{pieces: []string{"whole answer"}, jsonish: true, finish: "length"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	var got string
	res, err := p.Stream(context.Background(), testCall(), func(s string) { got += s })
	if err != nil || res.Text != "whole answer" || got != "whole answer" || !res.Truncated {
		t.Errorf("err=%v text=%q deltas=%q truncated=%v", err, res.Text, got, res.Truncated)
	}
}

func TestOpenAIChat_StatusAndFilter(t *testing.T) {
	fake := &fakeOpenAI{status: http.StatusUnauthorized}
	srv := httptest.NewServer(fake.handler(t))
	p := NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	if _, err := p.Stream(context.Background(), testCall(), func(string) {}); err == nil {
		t.Error("want error on 401")
	}
	srv.Close()

	fake = &fakeOpenAI{pieces: []string{"x"}, finish: "content_filter"}
	srv = httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p = NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	if res, err := p.Stream(context.Background(), testCall(), func(string) {}); err != nil || !res.Refused {
		t.Errorf("content_filter: err=%v refused=%v", err, res.Refused)
	}
}

func TestOpenAIChat_SharedTextOnlyInFirstUserTurn(t *testing.T) {
	fake := &fakeOpenAI{pieces: []string{"ok"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewOpenAIChatProvider(OpenAIChatConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	call := testCall()
	call.Messages = []ChatMessage{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"}}
	if _, err := p.Stream(context.Background(), call, func(string) {}); err != nil {
		t.Fatal(err)
	}
	msgs := fake.bodies[0]["messages"].([]any)
	var withMaterial int
	for _, m := range msgs[1:] {
		if strings.Contains(m.(map[string]any)["content"].(string), "<shared_context>") {
			withMaterial++
		}
	}
	if len(msgs) != 4 || withMaterial != 1 || !strings.HasSuffix(msgs[1].(map[string]any)["content"].(string), "\n\na") {
		t.Errorf("messages = %v", msgs)
	}
	if msgs[3].(map[string]any)["content"] != "c" {
		t.Errorf("last turn = %v", msgs[3])
	}
}
