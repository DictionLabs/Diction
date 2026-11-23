package chat

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const DefaultChatAnthropicModel = "claude-haiku-5-5"

func ChatEngineFromEnv(lookup func(key, fallback string) string) *ChatEngine {
	return ChatEngineFromEnvScoped(ChatScopeOpen, lookup)
}

func ChatEngineFromEnvScoped(defaultScope ChatScope, lookup func(key, fallback string) string) *ChatEngine {
	scope := chatScopeFromLookup(lookup, defaultScope)
	e, err := chatEngineFromLookup(lookup)
	if err != nil {
		log.Printf("chat: disabled: %v", err)
		return &ChatEngine{Limits: chatLimitsFromLookup(lookup), Scope: scope}
	}
	e.Scope = scope
	if e.Enabled() {
		log.Printf("chat: provider=%s scope=%s limits ctx=%d turns=%d out=%d compact_tokens=%d compact_messages=%d keep=%d",
			e.Provider.Name(), e.Scope, e.Limits.MaxContextChars, e.Limits.MaxTurns, e.Limits.MaxOutputTokens,
			e.Limits.CompactTokens, e.Limits.CompactMessages, e.Limits.KeepMessages)
	} else {
		log.Printf("chat: off (no CHAT_PROVIDER and no LLM configured)")
	}
	return e
}

func chatLimitsFromLookup(env func(key, fallback string) string) ChatLimits {
	intOr := func(key string, fallback int) int {
		if n, err := strconv.Atoi(strings.TrimSpace(env(key, ""))); err == nil {
			return n
		}
		return fallback
	}
	l := DefaultChatLimits()
	l.MaxContextChars = intOr("DICTION_CHAT_MAX_CONTEXT_CHARS", l.MaxContextChars)
	l.MaxTurns = intOr("DICTION_CHAT_MAX_TURNS", l.MaxTurns)
	l.MaxMessageChars = intOr("DICTION_CHAT_MAX_MESSAGE_CHARS", l.MaxMessageChars)
	l.MaxTotalChars = intOr("DICTION_CHAT_MAX_TOTAL_CHARS", l.MaxTotalChars)
	l.MaxOutputTokens = intOr("DICTION_CHAT_MAX_OUTPUT_TOKENS", l.MaxOutputTokens)
	l.CompactTokens = intOr("DICTION_CHAT_COMPACT_TOKENS", l.CompactTokens)
	l.CompactMessages = intOr("DICTION_CHAT_COMPACT_MESSAGES", l.CompactMessages)
	l.KeepMessages = intOr("DICTION_CHAT_KEEP_MESSAGES", l.KeepMessages)
	l.SummaryMaxTokens = intOr("DICTION_CHAT_SUMMARY_MAX_TOKENS", l.SummaryMaxTokens)
	l.CompactMaxTotalChars = intOr("DICTION_CHAT_COMPACT_MAX_TOTAL_CHARS", l.CompactMaxTotalChars)
	l.Timeout = time.Duration(intOr("DICTION_CHAT_TIMEOUT_MS", int(l.Timeout/time.Millisecond))) * time.Millisecond
	return l
}

type chatEnvSettings struct {
	provider, baseURL, apiKey, model, effort string
}

func (c *chatEnvSettings) adoptLLMFallback(env func(key, fallback string) string) bool {
	switch {
	case c.baseURL != "":
		c.provider = providerOpenAI
	case env("LLM_BASE_URL", "") != "" && env("LLM_MODEL", "") != "":
		c.provider = providerOpenAI
		c.baseURL = env("LLM_BASE_URL", "")
		if c.apiKey == "" {
			c.apiKey = env("LLM_API_KEY", "")
		}
		if c.model == "" {
			c.model = env("LLM_MODEL", "")
		}
		if c.effort == "" {
			c.effort = env("LLM_REASONING_EFFORT", "")
		}
	default:
		return false
	}
	return true
}

func (c *chatEnvSettings) newProvider(env func(key, fallback string) string) (ChatProvider, error) {
	switch c.provider {
	case "off", "none", "false":
		return nil, nil
	case "anthropic":
		if c.apiKey == "" {
			c.apiKey = env("ANTHROPIC_API_KEY", "")
		}
		if c.apiKey == "" {
			return nil, fmt.Errorf("CHAT_PROVIDER=anthropic needs CHAT_API_KEY or ANTHROPIC_API_KEY")
		}
		if c.model == "" {
			c.model = DefaultChatAnthropicModel
		}
		if c.effort == "" {
			c.effort = "low"
		}
		return NewAnthropicChatProvider(
			AnthropicChatConfig{APIKey: c.apiKey, BaseURL: c.baseURL, Model: c.model, Effort: c.effort}), nil
	case providerOpenAI:
		if c.baseURL == "" || c.model == "" {
			return nil, fmt.Errorf("CHAT_PROVIDER=openai needs CHAT_BASE_URL and CHAT_MODEL")
		}
		return NewOpenAIChatProvider(
			OpenAIChatConfig{BaseURL: c.baseURL, APIKey: c.apiKey, Model: c.model, ReasoningEffort: c.effort}), nil
	default:
		return nil, fmt.Errorf("unknown CHAT_PROVIDER %q (anthropic | openai | off)", c.provider)
	}
}

func chatEngineFromLookup(env func(key, fallback string) string) (*ChatEngine, error) {
	e := &ChatEngine{Limits: chatLimitsFromLookup(env)}
	c := chatEnvSettings{
		provider: strings.ToLower(strings.TrimSpace(env("CHAT_PROVIDER", ""))),
		baseURL:  strings.TrimSpace(env("CHAT_BASE_URL", "")),
		apiKey:   env("CHAT_API_KEY", ""),
		model:    strings.TrimSpace(env("CHAT_MODEL", "")),
		effort:   strings.TrimSpace(env("CHAT_EFFORT", "")),
	}
	if c.provider == "" && !c.adoptLLMFallback(env) {
		return e, nil
	}
	p, err := c.newProvider(env)
	if err != nil {
		return e, err
	}
	if p != nil {
		e.Provider = p
	}
	return e, nil
}

func chatScopeFromLookup(env func(key, fallback string) string, fallback ChatScope) ChatScope {
	switch v := ChatScope(strings.ToLower(strings.TrimSpace(env("DICTION_CHAT_SCOPE", "")))); v {
	case ChatScopeOpen, ChatScopeKeyboard:
		return v
	case "":
		return fallback
	default:
		log.Printf("chat: unknown DICTION_CHAT_SCOPE %q (keyboard | open), using %s", v, fallback)
		return fallback
	}
}
