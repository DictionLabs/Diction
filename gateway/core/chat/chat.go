package chat

import (
	"context"
	"net/http"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCall struct {
	System    string
	Context   string
	Summary   string
	Messages  []ChatMessage
	MaxTokens int
}

type ChatResult struct {
	Text             string
	Model            string
	StopReason       string
	Truncated        bool
	Refused          bool
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TTFT             time.Duration
}

type ChatProvider interface {
	Name() string
	Stream(ctx context.Context, call ChatCall, onDelta func(string)) (ChatResult, error)
}

type ChatLimits struct {
	MaxContextChars int
	MaxTurns        int
	MaxMessageChars int
	MaxTotalChars   int
	MaxOutputTokens int
	TitleMaxTokens  int
	Timeout         time.Duration
	MaxBodyBytes    int64

	CompactTokens        int
	CompactMessages      int
	KeepMessages         int
	SummaryMaxTokens     int
	MaxSummaryChars      int
	CompactMaxTotalChars int
}

func DefaultChatLimits() ChatLimits {
	return ChatLimits{
		MaxContextChars:      16000,
		MaxTurns:             12,
		MaxMessageChars:      4000,
		MaxTotalChars:        24000,
		MaxOutputTokens:      2048,
		TitleMaxTokens:       512,
		Timeout:              60 * time.Second,
		MaxBodyBytes:         256 << 10,
		CompactTokens:        12000,
		CompactMessages:      24,
		KeepMessages:         6,
		SummaryMaxTokens:     1024,
		MaxSummaryChars:      8000,
		CompactMaxTotalChars: 60000,
	}
}

const openAITitleMinTokens = 2048

func (l ChatLimits) titleMaxTokens(provider string) int {
	if provider == providerOpenAI && l.TitleMaxTokens < openAITitleMinTokens {
		return openAITitleMinTokens
	}
	return l.TitleMaxTokens
}

type ChatOutcome struct {
	Task         string
	Provider     string
	Stream       bool
	ContextChars int
	MessageChars int
	Turns        int
	Result       ChatResult
	Elapsed      time.Duration
	Err          error
}

type ChatEngine struct {
	Provider ChatProvider
	Limits   ChatLimits
	Scope    ChatScope
	OnResult func(ctx context.Context, o ChatOutcome)
	Admit    func(r *http.Request, a ChatAdmission) (done func(ChatResult, error), rej *ChatRejection)
}

type ChatAdmission struct {
	Task       string
	Stream     bool
	Provider   string
	MaxTokens  int
	Compact    bool
	InputBytes int
}

type ChatRejection struct {
	Status     int
	Code       string
	RetryAfter int
	Detail     map[string]any
}

type ChatScope string

const (
	ChatScopeOpen     ChatScope = "open"
	ChatScopeKeyboard ChatScope = "keyboard"
)

func (e *ChatEngine) system(insertMarkup bool) string {
	s := ChatSystemPrompt
	if insertMarkup {
		s = ChatInsertSystemPrompt
	}
	if e.Scope == ChatScopeKeyboard {
		s += "\n" + chatKeyboardScopeRule
	}
	return s
}

func (e *ChatEngine) Enabled() bool { return e != nil && e.Provider != nil }
