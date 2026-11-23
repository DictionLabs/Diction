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

type fakeAnthropic struct {
	mu       sync.Mutex
	bodies   []map[string]any
	headers  []http.Header
	pieces   []string
	stop     string
	status   int
	cacheHit int64
}

func (f *fakeAnthropic) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("anthropic path = %s, want /v1/messages", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		if f.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.status)
			io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"secret echo"}}`)
			return
		}
		stop := f.stop
		if stop == "" {
			stop = "end_turn"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(ev, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data) }
		send("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_read_input_tokens":%d,"cache_creation_input_tokens":34,"output_tokens":1}}}`, f.cacheHit))
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		for _, p := range f.pieces {
			b, _ := json.Marshal(p)
			send("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, b))
		}
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":9}}`, stop))
		send("message_stop", `{"type":"message_stop"}`)
	}
}

func TestAnthropicChat_StreamsTextUsageAndCaching(t *testing.T) {
	fake := &fakeAnthropic{pieces: []string{"I'm in, ", "but running late!"}, cacheHit: 900}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewAnthropicChatProvider(AnthropicChatConfig{APIKey: "k-test", BaseURL: srv.URL, Model: "claude-haiku-5-5", Effort: "low"})

	var deltas []string
	res, err := p.Stream(context.Background(), testCall(), func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if res.Text != "I'm in, but running late!" || len(deltas) != 2 {
		t.Errorf("text=%q deltas=%v", res.Text, deltas)
	}
	if res.InputTokens != 12 || res.CacheReadTokens != 900 || res.CacheWriteTokens != 34 || res.OutputTokens != 9 {
		t.Errorf("usage = in %d read %d write %d out %d", res.InputTokens, res.CacheReadTokens, res.CacheWriteTokens, res.OutputTokens)
	}
	if res.StopReason != "end_turn" || res.Truncated || res.Refused || res.TTFT <= 0 {
		t.Errorf("stop=%q truncated=%v refused=%v ttft=%v", res.StopReason, res.Truncated, res.Refused, res.TTFT)
	}

	if h := fake.headers[0]; h.Get("Authorization") != "Bearer k-test" && h.Get("X-Api-Key") != "k-test" {
		t.Error("API key not sent")
	}
	body := fake.bodies[0]
	if body["model"] != "claude-haiku-5-5" || body["max_tokens"].(float64) != 256 || body["stream"] != true {
		t.Errorf("model/max_tokens/stream = %v/%v/%v", body["model"], body["max_tokens"], body["stream"])
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Errorf("output_config = %v", body["output_config"])
	}
	if cc, _ := body["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Errorf("top-level cache_control = %v, want ephemeral (caches each turn's tail)", body["cache_control"])
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 {
		t.Fatalf("system blocks = %d, want 1: the shared text is not system material", len(sys))
	}
	if first := sys[0].(map[string]any); first["text"] != ChatSystemPrompt || first["cache_control"] != nil {
		t.Errorf("system[0] = %v", first)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", msgs)
	}
	turn := msgs[0].(map[string]any)
	blocks, _ := turn["content"].([]any)
	if turn["role"] != "user" || len(blocks) != 2 {
		t.Fatalf("first turn = %v, want user with [shared text, message]", turn)
	}
	material, said := blocks[0].(map[string]any), blocks[1].(map[string]any)
	if !strings.HasPrefix(material["text"].(string), "<shared_context>") || material["cache_control"] == nil {
		t.Errorf("first block must be the wrapped shared text with the breakpoint: %v", material)
	}
	if said["text"] != "reply, say I'm in but late" || said["cache_control"] != nil {
		t.Errorf("second block = %v", said)
	}
	if _, has := body["temperature"]; has {
		t.Error("temperature must not be sent (Haiku 5.5 rejects sampling params)")
	}
}

func TestAnthropicChat_NoContextPutsBreakpointOnInstructions(t *testing.T) {
	fake := &fakeAnthropic{pieces: []string{"ok"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewAnthropicChatProvider(AnthropicChatConfig{APIKey: "k", BaseURL: srv.URL, Model: "claude-haiku-5-5"})
	call := testCall()
	call.Context = ""
	if _, err := p.Stream(context.Background(), call, func(string) {}); err != nil {
		t.Fatal(err)
	}
	sys := fake.bodies[0]["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["cache_control"] == nil {
		t.Errorf("system = %v", sys)
	}
	if _, has := fake.bodies[0]["output_config"]; has {
		t.Error("empty Effort must leave output_config out (model default)")
	}
}

func TestAnthropicChat_StopReasons(t *testing.T) {
	for _, tc := range []struct {
		stop               string
		truncated, refused bool
	}{{"max_tokens", true, false}, {"refusal", false, true}} {
		fake := &fakeAnthropic{pieces: []string{"x"}, stop: tc.stop}
		srv := httptest.NewServer(fake.handler(t))
		p := NewAnthropicChatProvider(AnthropicChatConfig{APIKey: "k", BaseURL: srv.URL, Model: "claude-haiku-5-5"})
		res, err := p.Stream(context.Background(), testCall(), func(string) {})
		srv.Close()
		if err != nil || res.Truncated != tc.truncated || res.Refused != tc.refused {
			t.Errorf("%s: err=%v truncated=%v refused=%v", tc.stop, err, res.Truncated, res.Refused)
		}
	}
}

func TestAnthropicChat_HTTPErrorIsError(t *testing.T) {
	fake := &fakeAnthropic{status: http.StatusBadRequest}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p := NewAnthropicChatProvider(AnthropicChatConfig{APIKey: "k", BaseURL: srv.URL, Model: "claude-haiku-5-5"})
	if _, err := p.Stream(context.Background(), testCall(), func(string) {}); err == nil {
		t.Fatal("want an error on 400")
	}
}
