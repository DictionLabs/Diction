package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatHandler_AdmitRejects(t *testing.T) {
	p := &stubProvider{text: "hi"}
	e := &ChatEngine{Provider: p, Limits: DefaultChatLimits()}
	var seen ChatAdmission
	e.Admit = func(_ *http.Request, a ChatAdmission) (func(ChatResult, error), *ChatRejection) {
		seen = a
		return nil, &ChatRejection{Status: http.StatusTooManyRequests, Code: "chat_quota", RetryAfter: 42, Detail: map[string]any{"scope": "user"}}
	}
	body := `{"task":"chat","context":"shared","messages":[{"role":"user","content":"hello"}]}`
	rr := httptest.NewRecorder()
	ChatHandler(e)(rr, httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body)))
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "42" {
		t.Fatalf("got %d Retry-After=%q", rr.Code, rr.Header().Get("Retry-After"))
	}
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["error"] != "chat_quota" || got["scope"] != "user" {
		t.Fatalf("body %v", got)
	}
	if len(p.calls) != 0 {
		t.Fatal("provider called despite rejection")
	}
	if seen.Task != "chat" || seen.MaxTokens != DefaultChatLimits().MaxOutputTokens || seen.InputBytes < len("shared")+len("hello") {
		t.Fatalf("admission %+v", seen)
	}
}

func TestChatHandler_AdmitDoneGetsResult(t *testing.T) {
	p := &stubProvider{text: "hi there", res: ChatResult{InputTokens: 7, OutputTokens: 3}}
	e := &ChatEngine{Provider: p, Limits: DefaultChatLimits()}
	calls := 0
	var gotRes ChatResult
	e.Admit = func(_ *http.Request, _ ChatAdmission) (func(ChatResult, error), *ChatRejection) {
		return func(res ChatResult, _ error) { calls++; gotRes = res }, nil
	}
	for _, stream := range []bool{false, true} {
		calls = 0
		body := fmt.Sprintf(`{"task":"chat","context":"x","messages":[{"role":"user","content":"hello"}],"stream":%v}`, stream)
		rr := httptest.NewRecorder()
		ChatHandler(e)(rr, httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body)))
		if rr.Code != http.StatusOK || calls != 1 || gotRes.OutputTokens != p.res.OutputTokens {
			t.Fatalf("stream=%v: code %d done calls %d res %+v", stream, rr.Code, calls, gotRes)
		}
	}
}
