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

	"github.com/omachala/diction/gateway/core"
)

type llmConfig struct {
	Enabled         bool
	BaseURL         string
	APIKey          string
	Model           string
	Prompt          string
	ReasoningEffort string
}

func llmConfigFromEnv() llmConfig {
	baseURL := core.EnvOrDefault("LLM_BASE_URL", "")
	model := core.EnvOrDefault("LLM_MODEL", "")

	// Load prompt from env var or file path (if starts with /)
	prompt := core.EnvOrDefault("LLM_PROMPT", "")
	if strings.HasPrefix(prompt, "/") {
		data, err := os.ReadFile(prompt)
		if err != nil {
			log.Printf("LLM_PROMPT: failed to read file %s: %v", prompt, err)
			prompt = ""
		} else {
			prompt = strings.TrimSpace(string(data))
		}
	}

	enabled := baseURL != "" && model != ""

	if enabled && prompt == "" {
		log.Printf("WARNING: LLM enabled but LLM_PROMPT is empty — LLM will receive no system instructions")
	}

	return llmConfig{
		Enabled:         enabled,
		APIKey:          core.EnvOrDefault("LLM_API_KEY", ""),
		BaseURL:         baseURL,
		Model:           model,
		Prompt:          prompt,
		ReasoningEffort: core.EnvOrDefault("LLM_REASONING_EFFORT", ""),
	}
}

// process sends the transcript to the LLM and returns the cleaned result.
// Returns error on failure — caller falls back to raw transcript.
func (c llmConfig) process(ctx context.Context, transcript string) (string, error) {
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

	maxTokens := len(transcript)/2 + 200
	if maxTokens < 500 {
		maxTokens = 500
	}
	if maxTokens > 8192 {
		maxTokens = 8192
	}

	body, err := json.Marshal(request{
		Model: c.Model,
		Messages: []message{
			{Role: "system", Content: c.Prompt},
			{Role: "user", Content: transcript},
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

	timeoutSecs := 30 + len(transcript)/400
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
