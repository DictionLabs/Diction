package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIChatConfig struct {
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	HTTPClient      *http.Client
}

type openAIChatProvider struct {
	cfg  OpenAIChatConfig
	http *http.Client
}

func NewOpenAIChatProvider(cfg OpenAIChatConfig) ChatProvider {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &openAIChatProvider{cfg: cfg, http: hc}
}

func (p *openAIChatProvider) Name() string { return providerOpenAI }

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model               string              `json:"model"`
	Messages            []openAIChatMessage `json:"messages"`
	Stream              bool                `json:"stream"`
	StreamOptions       map[string]bool     `json:"stream_options,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
}

type openAIChatChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *openAIChatProvider) body(call ChatCall) openAIChatRequest {
	lead := call.Context
	if call.Summary != "" {
		if lead != "" {
			lead += "\n\n"
		}
		lead += call.Summary
	}
	msgs := []openAIChatMessage{{Role: "system", Content: call.System}}
	if lead != "" && len(call.Messages) > 0 && call.Messages[0].Role == roleAssistant {
		msgs = append(msgs, openAIChatMessage{Role: roleUser, Content: lead})
		lead = ""
	}
	for i, m := range call.Messages {
		content := m.Content
		if i == 0 && m.Role == roleUser && lead != "" {
			content = lead + "\n\n" + content
		}
		msgs = append(msgs, openAIChatMessage{Role: m.Role, Content: content})
	}
	return openAIChatRequest{
		Model:               p.cfg.Model,
		Messages:            msgs,
		Stream:              true,
		StreamOptions:       map[string]bool{"include_usage": true},
		MaxCompletionTokens: call.MaxTokens,
		ReasoningEffort:     p.cfg.ReasoningEffort,
	}
}

func (p *openAIChatProvider) Stream(ctx context.Context, call ChatCall, onDelta func(string)) (ChatResult, error) {
	res := ChatResult{Model: p.cfg.Model}
	payload, err := json.Marshal(p.body(call))
	if err != nil {
		return res, err
	}
	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	start := time.Now()
	resp, err := p.http.Do(req)
	if err != nil {
		return res, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return res, fmt.Errorf("chat provider status %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		return p.readWhole(resp.Body, res, start, onDelta)
	}

	var text strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch openAIChatChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Error != nil {
			return res, fmt.Errorf("chat provider stream error")
		}
		p.apply(&res, &text, ch, start, onDelta)
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	res.Text = text.String()
	return res, nil
}

func (p *openAIChatProvider) apply(res *ChatResult, text *strings.Builder, ch openAIChatChunk, start time.Time, onDelta func(string)) {
	if ch.Model != "" {
		res.Model = ch.Model
	}
	for _, c := range ch.Choices {
		if piece := c.Delta.Content; piece != "" {
			if res.TTFT == 0 {
				res.TTFT = time.Since(start)
			}
			text.WriteString(piece)
			onDelta(piece)
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			res.StopReason = *c.FinishReason
			res.Truncated = res.StopReason == "length"
			res.Refused = res.StopReason == "content_filter"
		}
	}
	if u := ch.Usage; u != nil {
		res.InputTokens = u.PromptTokens
		res.OutputTokens = u.CompletionTokens
		if u.PromptTokensDetails != nil {
			res.CacheReadTokens = u.PromptTokensDetails.CachedTokens
			res.InputTokens -= u.PromptTokensDetails.CachedTokens
		}
	}
}

func (p *openAIChatProvider) readWhole(body io.Reader, res ChatResult, start time.Time, onDelta func(string)) (ChatResult, error) {
	var whole struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 4<<20)).Decode(&whole); err != nil {
		return res, err
	}
	if whole.Model != "" {
		res.Model = whole.Model
	}
	if len(whole.Choices) > 0 {
		res.Text = whole.Choices[0].Message.Content
		res.StopReason = whole.Choices[0].FinishReason
		res.Truncated = res.StopReason == "length"
		res.Refused = res.StopReason == "content_filter"
		if res.Text != "" {
			res.TTFT = time.Since(start)
			onDelta(res.Text)
		}
	}
	if whole.Usage != nil {
		res.InputTokens, res.OutputTokens = whole.Usage.PromptTokens, whole.Usage.CompletionTokens
	}
	return res, nil
}
