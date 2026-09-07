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

	// Short text → minimum 4000. The floor exists for reasoning-class models, which
	// spend budget on hidden chain-of-thought before emitting any content; at the old
	// 500 floor they could spend it all and return empty, which reached users as a
	// failed edit on a self-hosted gateway running gpt-oss-20b.
	cfg.process(context.Background(), "hi")
	if receivedMaxTokens != 4000 {
		t.Errorf("short text: expected 4000 max_tokens, got %d", receivedMaxTokens)
	}

	// Very long text → capped at 8192
	cfg.process(context.Background(), strings.Repeat("x", 20000))
	if receivedMaxTokens != 8192 {
		t.Errorf("long text: expected 8192 max_tokens, got %d", receivedMaxTokens)
	}
}

// TestLLM_EditIntent_HasReasoningHeadroom pins the floor on the path that actually broke:
// an edit intent with a short spoken instruction, which produces the smallest user message
// and therefore the smallest budget, while a reasoning model's fixed thinking cost stays
// the same. Guards the whole intent surface, not just plain cleanup.
func TestLLM_EditIntent_HasReasoningHeadroom(t *testing.T) {
	var receivedMaxTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		receivedMaxTokens = req.MaxCompletionTokens
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	cfg := llmConfig{
		Enabled: true, BaseURL: srv.URL, Model: "test",
		Prompt: "CLEANUP", PromptEdit: "EDIT", PromptEditSelected: "EDIT_SEL",
	}
	// Carries a target for both edit intents (a selection and cursor context), since an edit
	// with nothing to edit is now a hard error and would never reach the token budget.
	const editable = `{"selected":"teh","before":"say ","after":" again"}`
	for _, intent := range []string{"", "transcribe", "edit", "edit-selected"} {
		receivedMaxTokens = 0
		if _, err := cfg.processWithIntent(
			context.Background(), "fix that", editable, intent); err != nil {
			t.Fatalf("intent=%q: %v", intent, err)
		}
		if receivedMaxTokens < 4000 {
			t.Errorf("intent=%q: max_tokens %d leaves no room for hidden reasoning tokens",
				intent, receivedMaxTokens)
		}
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

	// Same context for every intent: a selection and cursor text, so each edit intent has a
	// target and the test stays about which *prompt* was picked.
	const editable = `{"selected":"teh","before":"say ","after":" again"}`
	for _, tc := range tests {
		receivedPrompt = ""
		_, err := cfg.processWithIntent(context.Background(), "hello", editable, tc.intent)
		if err != nil {
			t.Errorf("intent=%q: unexpected error: %v", tc.intent, err)
			continue
		}
		if receivedPrompt != tc.want {
			t.Errorf("intent=%q: prompt: want %q, got %q", tc.intent, tc.want, receivedPrompt)
		}
	}
}

// TestLLM_ProcessWithIntent_LanguageHint pins the language hint added by
// .claude/plans/on-device-language-fix-plan.md: a concrete context.language appends
// "(Language: xx)" to the cleanup user message; "auto"/empty/absent don't; edit intents
// never get it (the transcript there is a spoken instruction, not the text to clean).
func TestLLM_ProcessWithIntent_LanguageHint(t *testing.T) {
	var receivedUserMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			if m.Role == "user" {
				receivedUserMsg = m.Content
			}
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
	}

	tests := []struct {
		name        string
		intent      string
		contextJSON string
		wantHint    bool
	}{
		{"concrete language, cleanup intent", "", `{"language":"cs"}`, true},
		{"concrete language, transcribe intent", "transcribe", `{"language":"cs"}`, true},
		{"auto sentinel", "", `{"language":"auto"}`, false},
		{"empty string", "", `{"language":""}`, false},
		{"absent field", "", `{}`, false},
		{"no context at all", "", "", false},
		// Both edit intents need a target now, or they fail before a hint could be added.
		{"concrete language, edit intent", "edit", `{"language":"cs","before":"a ","after":" b"}`, false},
		{"concrete language, edit-selected intent", "edit-selected", `{"language":"cs","selected":"a"}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receivedUserMsg = ""
			_, err := cfg.processWithIntent(context.Background(), "some text", tc.contextJSON, tc.intent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hasHint := strings.Contains(receivedUserMsg, "(Language: cs)")
			if hasHint != tc.wantHint {
				t.Errorf("(Language: cs) present = %v, want %v (user message: %q)", hasHint, tc.wantHint, receivedUserMsg)
			}
		})
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

// ── Formatting flag ───────────────────────────────────────────────────────────

// captureSystemPrompt runs processWithIntent against a stub LLM and returns the
// system prompt the gateway actually sent.
func captureSystemPrompt(t *testing.T, cfg llmConfig, contextJSON, intent string) string {
	t.Helper()
	var systemPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		for _, m := range req.Messages {
			if m.Role == "system" {
				systemPrompt = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	cfg.Enabled = true
	cfg.BaseURL = srv.URL
	cfg.Model = "test"
	if _, err := cfg.processWithIntent(context.Background(), "some text", contextJSON, intent); err != nil {
		t.Fatalf("processWithIntent: %v", err)
	}
	return systemPrompt
}

// Opt-out contract: absent formatting key means ON, matching older clients that
// never send it.
func TestFormatting_absentKey_appendsRules(t *testing.T) {
	cfg := llmConfig{Prompt: "CLEANUP", PromptFormatting: "|FORMATTING|"}
	got := captureSystemPrompt(t, cfg, `{"before":"x"}`, "")
	if !strings.Contains(got, "|FORMATTING|") {
		t.Errorf("formatting rules must be appended when the key is absent; got %q", got)
	}
}

func TestFormatting_true_appendsRules(t *testing.T) {
	cfg := llmConfig{Prompt: "CLEANUP", PromptFormatting: "|FORMATTING|"}
	got := captureSystemPrompt(t, cfg, `{"formatting":true}`, "")
	if !strings.Contains(got, "|FORMATTING|") {
		t.Errorf("formatting rules must be appended when formatting=true; got %q", got)
	}
}

// The only way a user with the toggle off avoids formatting, and the reason the
// rules live in a separate const: the base prompt must be byte-identical here.
func TestFormatting_false_omitsRules(t *testing.T) {
	cfg := llmConfig{Prompt: "CLEANUP", PromptFormatting: "|FORMATTING|"}
	got := captureSystemPrompt(t, cfg, `{"formatting":false}`, "")
	if strings.Contains(got, "|FORMATTING|") {
		t.Errorf("formatting=false must omit the rules; got %q", got)
	}
	if got != "CLEANUP" {
		t.Errorf("base prompt must be unchanged when formatting is off; got %q", got)
	}
}

// An edit instruction already states the shape of the result, so layout rules
// there would fight the user's own instruction.
func TestFormatting_editIntent_neverAppendsRules(t *testing.T) {
	cfg := llmConfig{PromptEdit: "EDIT", PromptFormatting: "|FORMATTING|"}
	// Cursor context included: an edit with no target now fails before a prompt is sent.
	got := captureSystemPrompt(t, cfg, `{"formatting":true,"before":"a ","after":" b"}`, "edit")
	if strings.Contains(got, "|FORMATTING|") {
		t.Errorf("edit intent must not carry formatting rules; got %q", got)
	}
}

func TestFormatting_emptyContext_appendsRules(t *testing.T) {
	cfg := llmConfig{Prompt: "CLEANUP", PromptFormatting: "|FORMATTING|"}
	got := captureSystemPrompt(t, cfg, "", "")
	if !strings.Contains(got, "|FORMATTING|") {
		t.Errorf("empty context must default to formatting ON; got %q", got)
	}
}

// ── Self-hosted Writing Tools parity (.claude/plans/selfhosted-writing-tools-parity-plan.md) ──
//
// These pin the three defects found during v13.0 release testing, all of which were silent:
// an edit intent answered with a transcribe mode, a cursor edit that passed the spoken
// instruction as its own subject, and My Words never decoding at all. A test for a silent bug
// that passes before the fix proves nothing, so each of these was run against unmodified code
// first — see `## Implementation Progress` in the plan for the fail-first output.

// captureUserMsg runs processWithIntent against a fake LLM and returns the user message the
// gateway actually sent. `reply` is what the fake LLM answers with.
func captureUserMsg(t *testing.T, cfg llmConfig, reply, text, contextJSON, intent string) (string, string, error) {
	t.Helper()
	var userMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		for _, m := range req.Messages {
			if m.Role == "user" {
				userMsg = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer srv.Close()

	cfg.Enabled = true
	cfg.BaseURL = srv.URL
	cfg.Model = "test"
	out, err := cfg.processWithIntent(context.Background(), text, contextJSON, intent)
	return userMsg, out, err
}

func parityTestConfig() llmConfig {
	return llmConfig{
		Prompt:             "CLEANUP_PROMPT",
		PromptEdit:         "EDIT_PROMPT",
		PromptEditSelected: "EDIT_SEL_PROMPT",
	}
}

// The cursor-edit bug itself: intent=edit with no selection used to send
// "Text: <spoken>\nInstruction: <spoken>" — the instruction as its own subject — which is
// where the 97 characters of nonsense came from. It must now send before‸after as the text.
func TestProcessWithIntent_CursorEdit(t *testing.T) {
	ctxJSON := `{"before":"Hello ","after":" world"}`
	userMsg, _, err := captureUserMsg(t, parityTestConfig(), "edited", "translate to French", ctxJSON, "edit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(userMsg, "Hello ‸ world") {
		t.Errorf("want cursor context %q in user message, got %q", "Hello ‸ world", userMsg)
	}
	if strings.Contains(userMsg, "Text: translate to French") {
		t.Errorf("the spoken instruction is being passed as its own subject: %q", userMsg)
	}
	if !strings.Contains(userMsg, "Instruction: translate to French") {
		t.Errorf("want the instruction present as an instruction, got %q", userMsg)
	}
}

// An edit with nothing to edit must error so the caller's failure path runs and the app shows
// "Couldn't apply edit". The alternative — today's behaviour — is typing the user's own
// instruction into their document, which is the worst available outcome.
func TestProcessWithIntent_EditRequiresTarget(t *testing.T) {
	cases := []struct {
		name        string
		intent      string
		contextJSON string
	}{
		{"cursor edit with no surrounding text", "edit", `{"before":"","after":""}`},
		{"cursor edit with no context at all", "edit", ""},
		{"edit-selected with no selection", "edit-selected", `{"before":"a","after":"b"}`},
		{"edit-selected with no context at all", "edit-selected", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureUserMsg(t, parityTestConfig(), "result", "make it formal", tc.contextJSON, tc.intent)
			if err == nil {
				t.Errorf("want an error so the failure path fires, got nil")
			}
		})
	}
}

// The gateway sends ‸ as the cursor marker; the app inserts results verbatim
// (KeyboardSessionBridge.applyEditResult). A model that echoes the marker would type it into
// the user's document, so the gateway strips it on the way out. Cloud does the same
// (gateway/llm.go:459).
func TestProcessWithIntent_StripsCursorMarker(t *testing.T) {
	cases := []struct {
		name        string
		intent      string
		contextJSON string
	}{
		{"edit", "edit", `{"before":"Hello ","after":" world"}`},
		{"edit-selected", "edit-selected", `{"selected":"Hello world"}`},
		{"cleanup", "", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := captureUserMsg(t, parityTestConfig(), "Hello ‸there world", "x", tc.contextJSON, tc.intent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(out, "‸") {
				t.Errorf("cursor marker reached the caller and would be typed into the document: %q", out)
			}
			if out != "Hello there world" {
				t.Errorf("want %q, got %q", "Hello there world", out)
			}
		})
	}
}

// My Words has never reached a self-hosted LLM: the app sends [{"word":"..."}] and the
// gateway declared []string, so the decode failed on that field alone and was
// //nolint:errcheck'd away. Strings must keep working too — AGENTS.md documents the field
// untyped, so a third-party client may well send them.
func TestProcessWithIntent_CustomWordsDecode(t *testing.T) {
	cases := []struct {
		name        string
		contextJSON string
	}{
		{"app object shape", `{"customWords":[{"word":"Ondrej"},{"word":"Diction"}]}`},
		{"third-party string shape", `{"customWords":["Ondrej","Diction"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userMsg, _, err := captureUserMsg(t, parityTestConfig(), "cleaned", "ondrej works on diction", tc.contextJSON, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(userMsg, "Ondrej") || !strings.Contains(userMsg, "Diction") {
				t.Errorf("custom words did not reach the LLM: %q", userMsg)
			}
		})
	}
}

// The user's own configured context must reach their own LLM (D8), and nothing else may.
// Absent fields stay absent: a one-line prompt must never be handed an empty label.
func TestProcessWithIntent_CleanupContext(t *testing.T) {
	full := `{"before":"a","after":"b","tone":"Friendly","profile":"An engineer",` +
		`"sessionContext":["earlier one","earlier two"],"clipboard":"pasted text",` +
		`"customWords":[{"word":"Ondrej"}]}`

	userMsg, _, err := captureUserMsg(t, parityTestConfig(), "cleaned", "the transcript", full, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Custom words:", "Ondrej", "Tone:", "Friendly", "An engineer",
		"Recent:", "earlier one", "Clipboard:", "pasted text"} {
		if !strings.Contains(userMsg, want) {
			t.Errorf("want %q in user message, got %q", want, userMsg)
		}
	}
	// The transcript stays first and unlabelled — the layout the language hint already set.
	if !strings.HasPrefix(userMsg, "the transcript") {
		t.Errorf("transcript must lead the user message, got %q", userMsg)
	}
	// Cursor context is deliberately NOT sent to cleanup in v13 (prompt-quality, not user
	// data — see the plan's Context section). Sending it would change the shape of nearly
	// every keyboard request and is the likeliest way to get the document echoed back.
	if strings.Contains(userMsg, "Cursor:") || strings.Contains(userMsg, "a‸b") {
		t.Errorf("cursor context must not reach the cleanup prompt, got %q", userMsg)
	}

	empty, _, err := captureUserMsg(t, parityTestConfig(), "cleaned", "the transcript", `{"before":"a","after":"b"}`, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, label := range []string{"Custom words:", "Tone:", "Recent:", "Clipboard:"} {
		if strings.Contains(empty, label) {
			t.Errorf("label %q must be omitted when the user configured nothing, got %q", label, empty)
		}
	}
}

// The compatibility promise, pinned rather than asserted: a self-hoster who has configured
// none of My Words, Tone, Profile, session or clipboard sends exactly the bytes they send
// today. Every keyboard dictation carries before/after/formatting/language, so this is the
// common case, not an edge case.
func TestProcessWithIntent_CleanupUnconfiguredIsByteIdentical(t *testing.T) {
	cases := []struct {
		name        string
		contextJSON string
		want        string
	}{
		{
			name:        "with a concrete language",
			contextJSON: `{"before":"a","after":"b","formatting":true,"language":"cs"}`,
			want:        "the transcript\n\n(Language: cs)",
		},
		{
			name:        "under auto-detect",
			contextJSON: `{"before":"a","after":"b","formatting":true,"language":"auto"}`,
			want:        "the transcript",
		},
		{
			name:        "no context at all",
			contextJSON: "",
			want:        "the transcript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userMsg, _, err := captureUserMsg(t, parityTestConfig(), "cleaned", "the transcript", tc.contextJSON, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if userMsg != tc.want {
				t.Errorf("user message changed for an unconfigured self-hoster:\n want %q\n got  %q", tc.want, userMsg)
			}
		})
	}
}

// One unreadable vocabulary entry must cost that entry and nothing else. A custom
// UnmarshalJSON that returns an error aborts the enclosing json.Unmarshal, which would take
// the user's tone, clipboard and session context down with it — a bigger version of the very
// bug this type was written to end.
func TestProcessWithIntent_MalformedCustomWordKeepsRestOfContext(t *testing.T) {
	ctxJSON := `{"customWords":[123,{"word":"Diction"}],"tone":"Friendly","clipboard":"pasted text"}`
	userMsg, _, err := captureUserMsg(t, parityTestConfig(), "cleaned", "the transcript", ctxJSON, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Tone: Friendly", "Clipboard: pasted text", "Diction"} {
		if !strings.Contains(userMsg, want) {
			t.Errorf("one bad custom word cost more than itself: want %q in %q", want, userMsg)
		}
	}
}
