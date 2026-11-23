package chat

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

type AnthropicChatConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Effort     string
	HTTPClient *http.Client
}

type anthropicChatProvider struct {
	client anthropic.Client
	cfg    AnthropicChatConfig
}

func NewAnthropicChatProvider(cfg AnthropicChatConfig) ChatProvider {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(1),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &anthropicChatProvider{client: anthropic.NewClient(opts...), cfg: cfg}
}

func (p *anthropicChatProvider) Name() string { return "anthropic" }

func (p *anthropicChatProvider) params(call ChatCall) anthropic.MessageNewParams {
	system := []anthropic.TextBlockParam{{Text: call.System}}

	var lead []anthropic.ContentBlockParamUnion
	if call.Context != "" {
		material := anthropic.NewTextBlock(call.Context)
		material.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
		lead = append(lead, material)
	}
	if call.Summary != "" {
		lead = append(lead, anthropic.NewTextBlock(call.Summary))
	}
	msgs := make([]anthropic.MessageParam, 0, len(call.Messages)+1)
	if len(lead) > 0 && len(call.Messages) > 0 && call.Messages[0].Role == roleAssistant {
		msgs = append(msgs, anthropic.NewUserMessage(lead...))
		lead = nil
	}
	for i, m := range call.Messages {
		if m.Role == roleAssistant {
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
			continue
		}
		if i == 0 && len(lead) > 0 {
			msgs = append(msgs, anthropic.NewUserMessage(append(lead, anthropic.NewTextBlock(m.Content))...))
			continue
		}
		msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
	}
	if call.Context == "" {
		system[0].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	params := anthropic.MessageNewParams{
		Model:        anthropic.Model(p.cfg.Model),
		MaxTokens:    int64(call.MaxTokens),
		System:       system,
		Messages:     msgs,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	if p.cfg.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(p.cfg.Effort)}
	}
	return params
}

func (p *anthropicChatProvider) Stream(ctx context.Context, call ChatCall, onDelta func(string)) (ChatResult, error) {
	res := ChatResult{Model: p.cfg.Model}
	start := time.Now()
	stream := p.client.Messages.NewStreaming(ctx, p.params(call))
	defer func() { _ = stream.Close() }()

	var msg anthropic.Message
	var text strings.Builder
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return res, err
		}
		if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if td, ok := d.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
				if res.TTFT == 0 {
					res.TTFT = time.Since(start)
				}
				text.WriteString(td.Text)
				onDelta(td.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return res, err
	}
	res.Text = text.String()
	if msg.Model != "" {
		res.Model = string(msg.Model)
	}
	res.StopReason = string(msg.StopReason)
	res.Truncated = msg.StopReason == anthropic.StopReasonMaxTokens
	res.Refused = msg.StopReason == anthropic.StopReasonRefusal
	res.InputTokens = msg.Usage.InputTokens
	res.OutputTokens = msg.Usage.OutputTokens
	res.CacheReadTokens = msg.Usage.CacheReadInputTokens
	res.CacheWriteTokens = msg.Usage.CacheCreationInputTokens
	return res, nil
}
