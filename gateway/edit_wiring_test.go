package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The v13 bug: buildMux's closure returned a hardcoded "" mode, so edits looked like dictations.

// TestPostProcessor_ReturnsModeForIntent: the wired closure must report the mode it ran.
func TestPostProcessor_ReturnsModeForIntent(t *testing.T) {
	srv := makeLLMServer(t, "the model's answer")
	cfg := llmConfig{
		Enabled: true, BaseURL: srv.URL, Model: "test",
		Prompt: "CLEANUP", PromptEdit: "EDIT", PromptEditSelected: "EDIT_SEL",
	}
	postProcess := cfg.postProcessor()
	const ctxJSON = `{"before":"say ","after":" again","selected":"teh"}`

	for _, tc := range []struct{ intent, wantMode string }{
		{"edit", "edit"},
		{"edit-selected", "edit-selected"},
		{"transcribe", "transcribe"},
		{"", "transcribe"},
	} {
		text, mode, err := postProcess(context.Background(), "fix that", ctxJSON, tc.intent)
		if err != nil {
			t.Errorf("intent=%q: unexpected error: %v", tc.intent, err)
			continue
		}
		if mode != tc.wantMode {
			t.Errorf("intent=%q: mode = %q, want %q", tc.intent, mode, tc.wantMode)
		}
		if text != "the model's answer" {
			t.Errorf("intent=%q: text = %q", tc.intent, text)
		}
	}

	// On failure the mode must still be right, never an empty string.
	dead := llmConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", Model: "test", PromptEdit: "EDIT"}
	if _, mode, err := dead.postProcessor()(context.Background(), "fix that", ctxJSON, "edit"); err == nil {
		t.Error("want an error from a dead LLM host, got none")
	} else if mode != "edit" {
		t.Errorf("mode on the failure path = %q, want %q", mode, "edit")
	}
}

// TestStreamWiring_EditIntentReturnsEditMode: STT, context, LLM, result through the real mux.
func TestStreamWiring_EditIntentReturnsEditMode(t *testing.T) {
	mux := splitWiringMux(t, "translate to French", "Bonjour le monde")

	frames := streamFramesWithContext(t, mux, "enhance=true&intent=edit",
		`{"before":"Hello ","after":" world"}`)

	if len(frames) != 1 {
		t.Fatalf("expected 1 result frame, got %d: %q", len(frames), frames)
	}
	var result struct {
		Text   string `json:"text"`
		Mode   string `json:"mode"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &result); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if result.Mode != "edit" {
		t.Errorf("mode = %q, want %q — the app applies an edit only on this value", result.Mode, "edit")
	}
	if result.Text != "Bonjour le monde" {
		t.Errorf("text = %q, want the edited text", result.Text)
	}
	if result.Status == "failed" {
		t.Errorf("unexpected failure status: %q", frames[0])
	}
}

// A plain dictation must carry mode="transcribe"; apps default a missing mode to it, so it is safe.
func TestStreamWiring_TranscribeIntentReturnsTranscribeMode(t *testing.T) {
	mux := splitWiringMux(t, "raw transcript here", "cleaned transcript here")

	frames := streamFramesWithContext(t, mux, "enhance=true", "")
	if len(frames) != 1 {
		t.Fatalf("expected 1 result frame, got %d: %q", len(frames), frames)
	}
	var result struct {
		Text string `json:"text"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &result); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if result.Mode != "transcribe" {
		t.Errorf("mode = %q, want %q", result.Mode, "transcribe")
	}
	if result.Text != "cleaned transcript here" {
		t.Errorf("text = %q", result.Text)
	}
}

// streamFramesWithContext dials the stream route, optionally sends a context frame, returns frames.
func streamFramesWithContext(t *testing.T, mux http.Handler, query, contextJSON string) []string {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/audio/stream?" + query
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if contextJSON != "" {
		if err := conn.Write(ctx, websocket.MessageText, []byte(contextJSON)); err != nil {
			t.Fatalf("write context: %v", err)
		}
	}
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

	var frames []string
	for {
		_, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return frames
		}
		frames = append(frames, string(data))
	}
}
