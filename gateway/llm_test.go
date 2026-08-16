package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLLM_ConfigFromEnv_Disabled(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("LLM_PROMPT")

	cfg := llmConfigFromEnv()
	if cfg.Enabled {
		t.Error("expected LLM disabled when no env vars set")
	}
}

func TestLLM_ConfigFromEnv_NeedsBaseURLAndModel(t *testing.T) {
	// Only BASE_URL → disabled
	os.Setenv("LLM_BASE_URL", "http://localhost:11434/v1")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("LLM_PROMPT")
	defer os.Unsetenv("LLM_BASE_URL")

	cfg := llmConfigFromEnv()
	if cfg.Enabled {
		t.Error("expected disabled with only BASE_URL")
	}

	// Only MODEL → disabled
	os.Unsetenv("LLM_BASE_URL")
	os.Setenv("LLM_MODEL", "gemma2:9b")
	defer os.Unsetenv("LLM_MODEL")

	cfg = llmConfigFromEnv()
	if cfg.Enabled {
		t.Error("expected disabled with only MODEL")
	}
}

func TestLLM_ConfigFromEnv_Enabled(t *testing.T) {
	os.Setenv("LLM_BASE_URL", "http://ollama:11434/v1")
	os.Setenv("LLM_MODEL", "gemma2:9b")
	os.Setenv("LLM_API_KEY", "test-key")
	os.Setenv("LLM_PROMPT", "Fix grammar.")
	os.Setenv("LLM_REASONING_EFFORT", "none")
	defer func() {
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
		os.Unsetenv("LLM_API_KEY")
		os.Unsetenv("LLM_PROMPT")
		os.Unsetenv("LLM_REASONING_EFFORT")
	}()

	cfg := llmConfigFromEnv()
	if !cfg.Enabled {
		t.Fatal("expected LLM enabled")
	}
	if cfg.BaseURL != "http://ollama:11434/v1" {
		t.Errorf("BaseURL: got %q", cfg.BaseURL)
	}
	if cfg.Model != "gemma2:9b" {
		t.Errorf("Model: got %q", cfg.Model)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("APIKey: got %q", cfg.APIKey)
	}
	if cfg.Prompt != "Fix grammar." {
		t.Errorf("Prompt: got %q", cfg.Prompt)
	}
	if cfg.ReasoningEffort != "none" {
		t.Errorf("ReasoningEffort: got %q", cfg.ReasoningEffort)
	}
}

func TestLLM_ConfigFromEnv_PromptFile(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.txt")
	os.WriteFile(promptFile, []byte("  Clean up this transcript.  \n"), 0644)

	os.Setenv("LLM_BASE_URL", "http://localhost:11434/v1")
	os.Setenv("LLM_MODEL", "gemma2:9b")
	os.Setenv("LLM_PROMPT", promptFile)
	defer func() {
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
		os.Unsetenv("LLM_PROMPT")
	}()

	cfg := llmConfigFromEnv()
	if cfg.Prompt != "Clean up this transcript." {
		t.Errorf("expected trimmed file content, got %q", cfg.Prompt)
	}
}

func TestLLM_ConfigFromEnv_PromptFileMissing(t *testing.T) {
	os.Setenv("LLM_BASE_URL", "http://localhost:11434/v1")
	os.Setenv("LLM_MODEL", "gemma2:9b")
	os.Setenv("LLM_PROMPT", "/nonexistent/path/prompt.txt")
	defer func() {
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
		os.Unsetenv("LLM_PROMPT")
	}()

	cfg := llmConfigFromEnv()
	// On file read failure the gateway falls back to the default cleanup prompt
	// rather than leaving the LLM with no system instructions.
	if cfg.Prompt != DefaultPromptCleanup {
		t.Errorf("expected DefaultPromptCleanup on file read failure, got %q", cfg.Prompt)
	}
}

func TestLLM_Process_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("expected /chat/completions path, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}

		var req struct {
			Model           string                           `json:"model"`
			Messages        []struct{ Role, Content string } `json:"messages"`
			Temperature     float64                          `json:"temperature"`
			ReasoningEffort string                           `json:"reasoning_effort"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Model != "gemma2:9b" {
			t.Errorf("model: got %q", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" || req.Messages[0].Content != "Fix grammar." {
			t.Errorf("system message: got %+v", req.Messages[0])
		}
		if req.Messages[1].Role != "user" || req.Messages[1].Content != "hello world" {
			t.Errorf("user message: got %+v", req.Messages[1])
		}
		if req.Temperature != 0.0 {
			t.Errorf("temperature: got %f", req.Temperature)
		}
		if req.ReasoningEffort != "none" {
			t.Errorf("reasoning_effort: got %q", req.ReasoningEffort)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "Hello, world."}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled:         true,
		BaseURL:         srv.URL,
		APIKey:          "test-key",
		Model:           "gemma2:9b",
		Prompt:          "Fix grammar.",
		ReasoningEffort: "none",
	}

	result, err := cfg.process(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, world." {
		t.Errorf("result: got %q", result)
	}
}

func TestLLM_Process_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("expected no Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test"}
	_, err := cfg.process(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLLM_Process_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "bad-model"}
	_, err := cfg.process(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status code in error, got: %v", err)
	}
}

func TestLLM_Process_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{}})
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test"}
	_, err := cfg.process(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestLLM_Process_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := cfg.process(ctx, "test")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestLLM_Process_TrimsWhitespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "\n  Hello, world.  \n"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test"}
	result, err := cfg.process(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, world." {
		t.Errorf("expected trimmed result, got %q", result)
	}
}

func TestLLM_Process_TrailingSlashInBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL + "/", Model: "test"}
	_, err := cfg.process(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLLM_Process_MaxTokensBounds(t *testing.T) {
	var receivedMaxTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		receivedMaxTokens = req.MaxCompletionTokens
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test"}

	// Short text → minimum 500
	cfg.process(context.Background(), "hi")
	if receivedMaxTokens != 500 {
		t.Errorf("short text: expected 500 max_tokens, got %d", receivedMaxTokens)
	}

	// Very long text → capped at 8192
	cfg.process(context.Background(), strings.Repeat("x", 20000))
	if receivedMaxTokens != 8192 {
		t.Errorf("long text: expected 8192 max_tokens, got %d", receivedMaxTokens)
	}
}

// --- New tests for extended llmConfig ---

func TestLLM_DefaultPrompts(t *testing.T) {
	os.Setenv("LLM_BASE_URL", "http://localhost:11434/v1")
	os.Setenv("LLM_MODEL", "test")
	os.Unsetenv("LLM_PROMPT")
	os.Unsetenv("LLM_PROMPT_EDIT")
	os.Unsetenv("LLM_PROMPT_EDIT_SELECTED")
	os.Unsetenv("LLM_PROMPT_SUGGEST")
	defer func() {
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
	}()

	cfg := llmConfigFromEnv()

	if cfg.Prompt != DefaultPromptCleanup {
		t.Errorf("Prompt: want DefaultPromptCleanup, got %q", cfg.Prompt)
	}
	if cfg.PromptEdit != DefaultPromptEdit {
		t.Errorf("PromptEdit: want DefaultPromptEdit, got %q", cfg.PromptEdit)
	}
	if cfg.PromptEditSelected != DefaultPromptEditSelected {
		t.Errorf("PromptEditSelected: want DefaultPromptEditSelected, got %q", cfg.PromptEditSelected)
	}
	if cfg.PromptSuggest != DefaultPromptSuggest {
		t.Errorf("PromptSuggest: want DefaultPromptSuggest, got %q", cfg.PromptSuggest)
	}
}

func TestLLM_ProcessWithIntent_PicksPrompt(t *testing.T) {
	var receivedPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			receivedPrompt = req.Messages[0].Content
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "result"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled:            true,
		BaseURL:            srv.URL,
		Model:              "test",
		Prompt:             "CLEANUP_PROMPT",
		PromptEdit:         "EDIT_PROMPT",
		PromptEditSelected: "EDIT_SEL_PROMPT",
		PromptSuggest:      "SUGGEST_PROMPT",
	}

	tests := []struct {
		intent string
		want   string
	}{
		{"", "CLEANUP_PROMPT"},
		{"transcribe", "CLEANUP_PROMPT"},
		{"edit", "EDIT_PROMPT"},
		{"edit-selected", "EDIT_SEL_PROMPT"},
	}

	for _, tc := range tests {
		receivedPrompt = ""
		_, err := cfg.processWithIntent(context.Background(), "hello", "", tc.intent)
		if err != nil {
			t.Errorf("intent=%q: unexpected error: %v", tc.intent, err)
			continue
		}
		if receivedPrompt != tc.want {
			t.Errorf("intent=%q: prompt: want %q, got %q", tc.intent, tc.want, receivedPrompt)
		}
	}
}

func TestLLM_SuggestFixes_SoftFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled:       true,
		BaseURL:       srv.URL,
		Model:         "test",
		PromptSuggest: DefaultPromptSuggest,
	}

	suggestions, err := cfg.suggestFixes(context.Background(), "hello", "", "", nil)
	if err != nil {
		t.Errorf("expected soft-fail (no error), got: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected empty slice on LLM error, got: %v", suggestions)
	}
}

func TestLLM_SuggestFixes_JSONArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `["option one","option two","option three"]`}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled:       true,
		BaseURL:       srv.URL,
		Model:         "test",
		PromptSuggest: DefaultPromptSuggest,
	}

	suggestions, err := cfg.suggestFixes(context.Background(), "hello", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 3 {
		t.Errorf("expected 3 suggestions, got %d: %v", len(suggestions), suggestions)
	}
	if suggestions[0] != "option one" {
		t.Errorf("first suggestion: want 'option one', got %q", suggestions[0])
	}
}

func TestLLM_SuggestFixes_NewlineSeparated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "first option\nsecond option\nthird option"}},
			},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled:       true,
		BaseURL:       srv.URL,
		Model:         "test",
		PromptSuggest: DefaultPromptSuggest,
	}

	suggestions, err := cfg.suggestFixes(context.Background(), "hello", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 3 {
		t.Errorf("expected 3 suggestions, got %d: %v", len(suggestions), suggestions)
	}
	if suggestions[1] != "second option" {
		t.Errorf("second suggestion: want 'second option', got %q", suggestions[1])
	}
}
