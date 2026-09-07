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
	PromptFormatting   string
	PromptSummary      string
}

// Default system prompts used when the corresponding env var is empty.
const (
	DefaultPromptCleanup = "You are a transcript cleanup tool. Fix grammar, punctuation, and remove filler words. " +
		"If a language is given, write in that language and correct wrong or missing accents or diacritics for it. Never translate. " +
		"Lines labelled \"Custom words\", \"Tone\", \"Recent\" or \"Clipboard\" may follow the transcript: they are context about the speaker, " +
		"never part of what you return. Return only the corrected transcript, nothing else."
	DefaultPromptEdit = "You are a text editor. The text contains " + cursorMarker + " marking where the user's cursor is. " +
		"Apply the user's spoken instruction to that text. Return only the full modified text, without the " + cursorMarker + " marker, and nothing else."
	DefaultPromptEditSelected = "You are a text editor. Apply the user's spoken instruction to the selected portion of text. Return only the edited selection, nothing else."
	DefaultPromptSuggest      = "Suggest 2-3 concise alternative phrasings or corrections for the selected text. Return a JSON array of strings only, no explanation."

	// DefaultPromptFormatting is appended to the cleanup prompt when the client
	// asks for formatting. Kept separate so the cleanup prompt stays byte-identical
	// when formatting is off, and so an operator can replace just this half.
	DefaultPromptFormatting = "\n\nAlso render the structure the speech carries, using line breaks and plain text only. " +
		"A spoken list becomes one item per line (\"- item\", or \"1. item\" when the speaker counts). " +
		"A clear change of topic becomes a paragraph break. " +
		"Keep short or casual dictation inline, with no list. " +
		"Never use markdown syntax (#, *, _), and never reword: the words stay exactly as spoken."

	// DefaultPromptSummary backs POST /v1/text/summarize.
	DefaultPromptSummary = "Summarise a voice note in ONE short line, so the user can recognise it later in a list. " +
		"Output exactly one line: no bullets, no preamble, no markdown. Target 120 characters or fewer. " +
		"Reply in the same language as the note. Lead with the topic, keep proper nouns, numbers and decisions, " +
		"and drop filler. Never invent details, and never write phrases like \"the user\" or \"this note\". " +
		"If the note is meaningless, output a single dash: -"
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
	promptFormatting := loadPromptEnv("LLM_PROMPT_FORMATTING", DefaultPromptFormatting)
	promptSummary := loadPromptEnv("LLM_PROMPT_SUMMARY", DefaultPromptSummary)

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
		PromptFormatting:   promptFormatting,
		PromptSummary:      promptSummary,
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

	// Output length is roughly input length, but the floor is what matters: a
	// reasoning-class model (gpt-oss, and most "thinking" models) spends tokens on
	// hidden chain-of-thought *before* writing any output, and charges that to the same
	// budget. At a 500 floor it can spend the lot thinking and return empty content,
	// which surfaces to the user as a failed edit. Short inputs are the worst case,
	// because they get the smallest budget while the reasoning cost stays fixed.
	//
	// 4000 is the floor production settled on for the same reason on 2026-09-02, after
	// measuring 900-2000 reasoning tokens burned on inputs that had only ~200 budgeted.
	// Costs nothing when the model does not reason: max_completion_tokens is a ceiling,
	// not a reservation, so a non-reasoning model still stops when it stops.
	maxTokens := len(userMsg)/2 + 200
	if maxTokens < 4000 {
		maxTokens = 4000
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

// postProcessor returns the Writing Tools closure the audio paths call: the WebSocket
// (core/streaming.go, inline and split answer) and the HTTP transcriptions proxy
// (core/proxy.go). It reports the mode it ran alongside the text, which is what lets the app
// tell an applied edit from a dictation.
//
// A named constructor rather than a closure literal inside buildMux for one reason: the
// closure IS the thing that broke — it returned a hardcoded "" mode — and a local inside
// buildMux cannot be called from a test. See TestPostProcessor_ReturnsModeForIntent.
//
// The mode is returned on the error path too, even though every caller currently derives its
// own failure shape from the intent it already holds (core/streaming.go:918,
// core/proxy.go:600). Returning it here means a future caller that trusts this value gets a
// correct one rather than an empty string.
func (c llmConfig) postProcessor() func(ctx context.Context, transcript, contextJSON, intent string) (string, string, error) {
	return func(ctx context.Context, transcript, contextJSON, intent string) (string, string, error) {
		result, err := c.processWithIntent(ctx, transcript, contextJSON, intent)
		return result, modeForIntent(intent), err
	}
}

// cursorMarker sits where the user's cursor is, in the text sent for a cursor edit. The app
// uses the same character when it caches the context it will later replace
// (KeyboardCommands.swift), and the cloud build has sent it in production for a long time.
const cursorMarker = "‸"

// Context caps. All measured in runes, never bytes: slicing a byte count through a multibyte
// character produces mojibake, and this data is routinely Czech, Polish, Japanese or emoji.
// The counts match the cloud's (gateway/llm.go) so a self-hoster sees the same volume of
// context, and they exist so a long session cannot crowd the transcript out of a community
// gateway's 8192-token ceiling.
const (
	maxClipboardRunes  = 1000
	maxToneRunes       = 500
	maxCustomWords     = 50
	maxSessionMessages = 5
)

// customWord is one My Words entry.
//
// Wire-tolerant by necessity: the app sends objects (`[{"word":"Diction"}]`), while the public
// wire contract in AGENTS.md documents `customWords` as an untyped array, so a third-party
// client may reasonably send bare strings. Accepting only one shape is precisely the bug this
// type exists to fix — the gateway used to declare []string, the app sent objects, the decode
// failed on that one field, and My Words silently never reached any self-hosted LLM.
type customWord struct {
	Word     string
	Variants []string
}

// UnmarshalJSON never fails. An entry it cannot read decodes to an empty word, which
// formatCustomWords skips.
//
// This is not laziness, it is the whole lesson of the bug: an error here aborts the enclosing
// json.Unmarshal, so one unreadable vocabulary entry would silently take the user's tone,
// clipboard and session context down with it — a bigger version of the failure this type was
// written to end. One bad entry costs one word.
func (w *customWord) UnmarshalJSON(data []byte) error {
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		w.Word = plain
		return nil
	}
	var obj struct {
		Word     string   `json:"word"`
		Variants []string `json:"variants"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		log.Printf("LLM custom words: skipping unreadable entry: %v", err)
		return nil
	}
	w.Word, w.Variants = obj.Word, obj.Variants
	return nil
}

// transcriptionContext is the structured context blob the client sends alongside a transcript.
// Every field is optional; a client that sends none of it gets exactly the request it did
// before any of these fields existed.
type transcriptionContext struct {
	Before   string `json:"before"`
	After    string `json:"after"`
	Selected string `json:"selected"`
	// The user's own configured data. Forwarding it is what makes My Words, Tone, About You,
	// session context and clipboard context work on a self-hosted gateway at all.
	CustomWords    []customWord `json:"customWords"`
	Tone           string       `json:"tone"`
	Profile        string       `json:"profile"`
	SessionContext []string     `json:"sessionContext"`
	Clipboard      string       `json:"clipboard"`
	// Opt-out, matching the app's wire contract: absent (older clients) or
	// true means formatting is ON; only an explicit false disables it.
	Formatting *bool `json:"formatting,omitempty"`
	// Language is a hint for the cleanup prompt: write in this language, fix wrong/missing
	// diacritics for it, never translate. "auto"/empty mean "infer" — see
	// `core.IsConcreteLanguage`.
	Language string `json:"language,omitempty"`
}

func (tc transcriptionContext) formattingEnabled() bool {
	return tc.Formatting == nil || *tc.Formatting
}

// processWithIntent picks the right prompt and builds the user message based on intent,
// then calls processWithPrompt.
func (c llmConfig) processWithIntent(ctx context.Context, text, contextJSON, intent string) (string, error) {
	var tc transcriptionContext
	if contextJSON != "" {
		// Best-effort by design: an older or third-party client may send a partial blob, and a
		// missing field should cost that one feature, not the whole request.
		json.Unmarshal([]byte(contextJSON), &tc) //nolint:errcheck
	}

	var prompt, userMsg string
	var err error
	switch intent {
	case "edit":
		prompt = c.PromptEdit
		userMsg, err = cursorEditUserMsg(tc, text)
	case "edit-selected":
		prompt = c.PromptEditSelected
		userMsg, err = selectionEditUserMsg(tc, text)
	default: // "" or "transcribe"
		// Formatting only applies to cleanup. An edit instruction already says
		// what shape the result should take, so appending layout rules there
		// would fight the user's own instruction.
		prompt = c.Prompt
		if tc.formattingEnabled() {
			prompt += c.PromptFormatting
		}
		userMsg = cleanupUserMsg(tc, text)
	}
	if err != nil {
		return "", err
	}

	result, err := c.processWithPrompt(ctx, prompt, userMsg)
	if err != nil {
		return "", err
	}
	// The marker is a gateway artefact the user never typed, and the app inserts results
	// verbatim (KeyboardSessionBridge.applyEditResult), so a model that echoes it would type it
	// into the document. Strip on every intent: it can only appear if we or the model put it
	// there. Same guard as the cloud's context-edit path.
	return strings.ReplaceAll(result, cursorMarker, ""), nil
}

// cursorEditUserMsg builds the message for a cursor edit: the text around the cursor is the
// subject, the transcript is the instruction to apply to it.
//
// Erroring on a target-less edit is deliberate. The caller turns an edit-intent error into
// {text:"", mode:<intent>, status:"failed"} (core/streaming.go, core/proxy.go), which the app
// surfaces as "Couldn't apply edit". The alternative is what this build used to do — send the
// instruction as its own subject and insert whatever came back, which typed the user's own
// words into their document.
func cursorEditUserMsg(tc transcriptionContext, instruction string) (string, error) {
	if tc.Before == "" && tc.After == "" {
		return "", fmt.Errorf("edit: no text around the cursor to edit")
	}
	return fmt.Sprintf("Text: %s%s%s\nInstruction: %s",
		tc.Before, cursorMarker, tc.After, instruction), nil
}

// selectionEditUserMsg builds the message for an edit applied to a selection.
//
// Note this deliberately diverges from the cloud, which falls back to mode="transcribe" and
// returns the raw transcript when the selection is missing — that inserts the instruction.
// Failing visibly is the better trade; do not "align" it. In practice the branch is close to
// unreachable from the app: the keyboard only arms edit-selected with a non-empty selection
// of at most 3000 characters (KeyboardCommands.swift).
func selectionEditUserMsg(tc transcriptionContext, instruction string) (string, error) {
	if tc.Selected == "" {
		return "", fmt.Errorf("edit-selected: no selected text to edit")
	}
	msg := fmt.Sprintf("Text: %s\nInstruction: %s", tc.Selected, instruction)
	if tc.Before != "" || tc.After != "" {
		msg += fmt.Sprintf("\nContext before: %s\nContext after: %s", tc.Before, tc.After)
	}
	return msg, nil
}

// cleanupUserMsg builds the cleanup message: the transcript first and unlabelled, then one
// labelled block per piece of context the user actually configured, then the language hint.
//
// Two rules hold this together. Every block is omitted when empty, so a self-hoster who has
// configured none of these sends byte-for-byte the request they sent before this existed
// (pinned by TestProcessWithIntent_CleanupUnconfiguredIsByteIdentical). And the cursor context
// is deliberately absent: unlike the rest it is not the user's own data but a prompt-quality
// feature that only pays off with a prompt written for it, and sending it to a one-line
// default prompt is the likeliest way to get the surrounding document echoed back and inserted
// twice. The edit intents, where the cursor IS the subject, are where it belongs.
func cleanupUserMsg(tc transcriptionContext, text string) string {
	var blocks []string
	if words := formatCustomWords(tc.CustomWords); words != "" {
		blocks = append(blocks, "Custom words: "+words)
	}
	// Tone says how to write, Profile says who the user is. One block: two overlapping
	// concepts are harder for a small model to juggle than one.
	if tone := joinNonEmpty("\n", tc.Tone, tc.Profile); tone != "" {
		blocks = append(blocks, "Tone: "+truncateRunes(tone, maxToneRunes))
	}
	if len(tc.SessionContext) > 0 {
		recent := tc.SessionContext
		if len(recent) > maxSessionMessages {
			recent = recent[len(recent)-maxSessionMessages:]
		}
		blocks = append(blocks, "Recent:\n"+strings.Join(recent, "\n"))
	}
	if tc.Clipboard != "" {
		blocks = append(blocks, "Clipboard: "+truncateRunes(tc.Clipboard, maxClipboardRunes))
	}

	userMsg := text
	if len(blocks) > 0 {
		userMsg += "\n\n" + strings.Join(blocks, "\n")
	}
	if core.IsConcreteLanguage(tc.Language) {
		userMsg += "\n\n(Language: " + tc.Language + ")"
	}
	return userMsg
}

// formatCustomWords renders My Words as "word, other (also heard as: variant)". Variants are
// legacy — the app stopped sending them — but old rows and other clients may still carry them.
func formatCustomWords(words []customWord) string {
	if len(words) > maxCustomWords {
		log.Printf("LLM custom words: truncating %d → %d", len(words), maxCustomWords)
		words = words[:maxCustomWords]
	}
	rendered := make([]string, 0, len(words))
	for _, w := range words {
		if w.Word == "" {
			continue
		}
		if len(w.Variants) > 0 {
			rendered = append(rendered, w.Word+" (also heard as: "+strings.Join(w.Variants, ", ")+")")
			continue
		}
		rendered = append(rendered, w.Word)
	}
	return strings.Join(rendered, ", ")
}

// truncateRunes caps a string by rune count. Byte slicing would split a multibyte character
// and hand the model mojibake.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// joinNonEmpty joins only the parts that carry something, so an absent half never leaves a
// stray separator behind.
func joinNonEmpty(sep string, parts ...string) string {
	present := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			present = append(present, p)
		}
	}
	return strings.Join(present, sep)
}

// summarise returns a one-line summary of a whole voice note.
//
// Whole-note operation: unlike cleanup and edit there is no cursor, selection or
// surrounding context, because a saved note has none. The language tag is a hint
// only; the prompt already says to reply in the note's own language, so "auto"
// and empty are both fine and are simply not passed on.
func (c llmConfig) summarise(ctx context.Context, text, language string) (string, error) {
	userMsg := text
	if language != "" && language != "auto" {
		userMsg += "\n\n(Language: " + language + ")"
	}
	return c.processWithPrompt(ctx, c.PromptSummary, userMsg)
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
