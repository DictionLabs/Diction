package chat

import (
	"testing"
	"time"
)

func envMap(m map[string]string) func(string, string) string {
	return func(k, fb string) string {
		if v, ok := m[k]; ok {
			return v
		}
		return fb
	}
}

func TestChatEngineFromLookup(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		provider string
		wantErr  bool
		check    func(t *testing.T, p ChatProvider)
	}{
		{name: "nothing set", env: nil},
		{name: "explicit off beats LLM", env: map[string]string{"CHAT_PROVIDER": "off", "LLM_BASE_URL": "http://x", "LLM_MODEL": "m"}},
		{name: "inherits Writing Tools LLM", env: map[string]string{"LLM_BASE_URL": "http://ollama:11434/v1", "LLM_MODEL": "llama3", "LLM_API_KEY": "lk", "LLM_REASONING_EFFORT": "low"}, provider: "openai",
			check: func(t *testing.T, p ChatProvider) {
				c := p.(*openAIChatProvider).cfg
				if c.BaseURL != "http://ollama:11434/v1" || c.Model != "llama3" || c.APIKey != "lk" || c.ReasoningEffort != "low" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{name: "CHAT_MODEL overrides inherited model", env: map[string]string{"LLM_BASE_URL": "http://x/v1", "LLM_MODEL": "small", "CHAT_MODEL": "big"}, provider: "openai",
			check: func(t *testing.T, p ChatProvider) {
				if m := p.(*openAIChatProvider).cfg.Model; m != "big" {
					t.Errorf("model = %q", m)
				}
			}},
		{name: "own openai backend, polish untouched", env: map[string]string{"CHAT_BASE_URL": "https://api.openai.com/v1", "CHAT_MODEL": "gpt-5-mini", "CHAT_API_KEY": "sk", "LLM_BASE_URL": "http://ollama/v1", "LLM_MODEL": "llama3"}, provider: "openai",
			check: func(t *testing.T, p ChatProvider) {
				c := p.(*openAIChatProvider).cfg
				if c.BaseURL != "https://api.openai.com/v1" || c.APIKey != "sk" || c.ReasoningEffort != "" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{name: "anthropic defaults", env: map[string]string{"CHAT_PROVIDER": "Anthropic", "ANTHROPIC_API_KEY": "ak"}, provider: "anthropic",
			check: func(t *testing.T, p ChatProvider) {
				c := p.(*anthropicChatProvider).cfg
				if c.Model != DefaultChatAnthropicModel || c.Effort != "low" || c.APIKey != "ak" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{name: "anthropic CHAT_API_KEY wins", env: map[string]string{"CHAT_PROVIDER": "anthropic", "CHAT_API_KEY": "ck", "ANTHROPIC_API_KEY": "ak", "CHAT_MODEL": "claude-sonnet-5-5", "CHAT_EFFORT": "medium"}, provider: "anthropic",
			check: func(t *testing.T, p ChatProvider) {
				c := p.(*anthropicChatProvider).cfg
				if c.APIKey != "ck" || c.Model != "claude-sonnet-5-5" || c.Effort != "medium" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{name: "anthropic without key", env: map[string]string{"CHAT_PROVIDER": "anthropic"}, wantErr: true},
		{name: "openai without model", env: map[string]string{"CHAT_PROVIDER": "openai", "CHAT_BASE_URL": "http://x"}, wantErr: true},
		{name: "unknown provider", env: map[string]string{"CHAT_PROVIDER": "gemini"}, wantErr: true},
	}
	for _, tc := range cases {
		e, err := chatEngineFromLookup(envMap(tc.env))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v", tc.name, err)
			continue
		}
		if tc.provider == "" {
			if e.Enabled() {
				t.Errorf("%s: want off, got %s", tc.name, e.Provider.Name())
			}
			continue
		}
		if !e.Enabled() || e.Provider.Name() != tc.provider {
			t.Errorf("%s: provider = %v", tc.name, e.Provider)
			continue
		}
		if tc.check != nil {
			tc.check(t, e.Provider)
		}
	}
}

func TestChatEngineFromLookup_Limits(t *testing.T) {
	t.Setenv("DICTION_CHAT_MAX_TURNS", "99")
	e, err := chatEngineFromLookup(envMap(map[string]string{
		"DICTION_CHAT_MAX_CONTEXT_CHARS": "8000",
		"DICTION_CHAT_TIMEOUT_MS":        "1500",
		"DICTION_CHAT_MAX_OUTPUT_TOKENS": "not a number",
	}))
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultChatLimits()
	if e.Limits.MaxContextChars != 8000 || e.Limits.Timeout != 1500*time.Millisecond {
		t.Errorf("limits = %+v", e.Limits)
	}
	if e.Limits.MaxTurns != def.MaxTurns || e.Limits.MaxOutputTokens != def.MaxOutputTokens {
		t.Errorf("unset or invalid keys must keep the defaults: %+v", e.Limits)
	}
}
