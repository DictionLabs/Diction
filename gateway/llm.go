package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
)

type llmConfig struct {
	Enabled            bool
	BaseURL            string
	APIKey             string
	Model              string
	Prompt             string
	ReasoningEffort    string
	PromptEdit         string
	PromptEditSelected string
	PromptSuggest      string
}

// Default system prompts used when the corresponding env var is empty.
const (
	DefaultPromptCleanup      = "You are a transcript cleanup tool. Fix grammar, punctuation, and remove filler words. Return only the corrected text, nothing else."
	DefaultPromptEdit         = "You are a text editor. Apply the user's spoken instruction to the text. Return only the edited result, nothing else."
	DefaultPromptEditSelected = "You are a text editor. Apply the user's spoken instruction to the selected portion of text. Return only the edited selection, nothing else."
	DefaultPromptSuggest      = "Suggest 2-3 concise alternative phrasings or corrections for the selected text. Return a JSON array of strings only, no explanation."
)

// loadPromptEnv reads a prompt from an env var. If the value starts with /,
// it is treated as a file path. Returns defaultVal when the env var is empty.
func loadPromptEnv(key, defaultVal string) string {
	val := core.EnvOrDefault(key, "")
	if val == "" {
		return defaultVal
	}
	if strings.HasPrefix(val, "/") {
		data, err := os.ReadFile(val)
		if err != nil {
			log.Printf("%s: failed to read file %s: %v", key, val, err)
			return defaultVal
		}
		return strings.TrimSpace(string(data))
	}
	return val
}

func llmConfigFromEnv() llmConfig {
	baseURL := core.EnvOrDefault("LLM_BASE_URL", "")
	model := core.EnvOrDefault("LLM_MODEL", "")

	enabled := baseURL != "" && model != ""

	// Load all prompts; each falls back to its exported default when unset.
	prompt := loadPromptEnv("LLM_PROMPT", DefaultPromptCleanup)
	promptEdit := loadPromptEnv("LLM_PROMPT_EDIT", DefaultPromptEdit)
	promptEditSelected := loadPromptEnv("LLM_PROMPT_EDIT_SELECTED", DefaultPromptEditSelected)
	promptSuggest := loadPromptEnv("LLM_PROMPT_SUGGEST", DefaultPromptSuggest)

	return llmConfig{
		Enabled:            enabled,
		APIKey:             core.EnvOrDefault("LLM_API_KEY", ""),
		BaseURL:            baseURL,
		Model:              model,
		Prompt:             prompt,
		ReasoningEffort:    core.EnvOrDefault("LLM_REASONING_EFFORT", ""),
		PromptEdit:         promptEdit,
		PromptEditSelected: promptEditSelected,
		PromptSuggest:      promptSuggest,
	}
}

// processWithPrompt sends userMsg to the LLM under the given system prompt.
// It is the shared HTTP call used by process, processWithIntent, and suggestFixes.
func (c llmConfig) processWithPrompt(ctx context.Context, prompt, userMsg string) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type request struct {
		Model               string    `json:"model"`
		Messages            []message `json:"messages"`
		MaxCompletionTokens int       `json:"max_completion_tokens"`
		Temperature         float64   `json:"temperature"`
		ReasoningEffort     string    `json:"reasoning_effort,omitempty"`
	}

	maxTokens := len(userMsg)/2 + 200
	if maxTokens < 500 {
		maxTokens = 500
	}
	if maxTokens > 8192 {
		maxTokens = 8192
	}

	body, err := json.Marshal(request{
		Model: c.Model,
		Messages: []message{
			{Role: "system", Content: prompt},
			{Role: "user", Content: userMsg},
		},
		MaxCompletionTokens: maxTokens,
		Temperature:         0.0,
		ReasoningEffort:     c.ReasoningEffort,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	timeoutSecs := 30 + len(userMsg)/400
	if timeoutSecs > 120 {
		timeoutSecs = 120
	}
	client := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm api error %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty llm response")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

// process sends the transcript to the LLM using the cleanup prompt and returns the cleaned result.
// Returns error on failure -- caller falls back to raw transcript.
func (c llmConfig) process(ctx context.Context, transcript string) (string, error) {
	return c.processWithPrompt(ctx, c.Prompt, transcript)
}

// processWithIntent picks the right prompt and builds the user message based on intent,
// then calls processWithPrompt.
func (c llmConfig) processWithIntent(ctx context.Context, text, contextJSON, intent string) (string, error) {
	var tc struct {
		Before      string   `json:"before"`
		After       string   `json:"after"`
		Selected    string   `json:"selected"`
		CustomWords []string `json:"customWords"`
	}
	if contextJSON != "" {
		json.Unmarshal([]byte(contextJSON), &tc) //nolint:errcheck
	}

	// Pick prompt by intent.
	var prompt string
	switch intent {
	case "edit":
		prompt = c.PromptEdit
	case "edit-selected":
		prompt = c.PromptEditSelected
	default: // "" or "transcribe"
		prompt = c.Prompt
	}

	// Build user message based on intent.
	var userMsg string
	switch intent {
	case "edit", "edit-selected":
		if tc.Selected != "" {
			userMsg = fmt.Sprintf("Text: %s\nInstruction: %s", tc.Selected, text)
		} else {
			userMsg = fmt.Sprintf("Text: %s\nInstruction: %s", text, text)
		}
		if tc.Before != "" || tc.After != "" {
			userMsg += fmt.Sprintf("\nContext before: %s\nContext after: %s", tc.Before, tc.After)
		}
	default:
		userMsg = text
	}

	return c.processWithPrompt(ctx, prompt, userMsg)
}

// suggestFixes calls the LLM with the suggest prompt and returns up to 3 alternatives.
// Always soft-fails: on any error it returns an empty slice, never an error.
func (c llmConfig) suggestFixes(ctx context.Context, selected, before, after string, customWords []string) ([]string, error) {
	userMsg := fmt.Sprintf("Selected: %s", selected)
	if before != "" || after != "" {
		userMsg += fmt.Sprintf("\nContext before: %s\nContext after: %s", before, after)
	}
	if len(customWords) > 0 {
		userMsg += fmt.Sprintf("\nCustom words: %s", strings.Join(customWords, ", "))
	}

	result, err := c.processWithPrompt(ctx, c.PromptSuggest, userMsg)
	if err != nil {
		return []string{}, nil // soft-fail
	}

	// Try JSON array parse first.
	var items []string
	if err := json.Unmarshal([]byte(result), &items); err == nil {
		return items, nil
	}

	// Fallback: newline-separated.
	lines := strings.Split(strings.TrimSpace(result), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
