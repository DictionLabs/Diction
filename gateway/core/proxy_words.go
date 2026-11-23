package core

import (
	"encoding/json"
	"regexp"
	"strings"
)

func buildWhisperPrompt(contextJSON string) string {
	words := customWordsFromContext(contextJSON)
	if len(words) == 0 {
		return ""
	}
	return whisperPromptLead + strings.Join(words, ", ") + "."
}

func CustomWordsFromContext(contextJSON string) []string {
	return customWordsFromContext(contextJSON)
}

func customWordsFromContext(contextJSON string) []string {
	if contextJSON == "" {
		return nil
	}
	var ctx struct {
		CustomWords []struct {
			Word string `json:"word"`
		} `json:"customWords"`
	}
	if err := json.Unmarshal([]byte(contextJSON), &ctx); err != nil || len(ctx.CustomWords) == 0 {
		return nil
	}
	words := make([]string, 0, len(ctx.CustomWords))
	for _, cw := range ctx.CustomWords {
		if cw.Word != "" {
			words = append(words, cw.Word)
		}
	}
	return words
}

const customWordsField = "custom_words"

const maxCustomWordsField = 100

func buildCustomWordsField(contextJSON string) string {
	words := customWordsFromContext(contextJSON)
	if len(words) == 0 {
		return ""
	}
	if len(words) > maxCustomWordsField {
		words = words[:maxCustomWordsField]
	}
	b, err := json.Marshal(words)
	if err != nil {
		return ""
	}
	return string(b)
}

func customWordsFor(b *Backend, field string) string {
	if b == nil || !b.AcceptsCustomWords {
		return ""
	}
	return field
}

const whisperPromptLead = "The following words are spelled exactly as shown: "

var whisperPromptEchoRE = regexp.MustCompile(`(?i)the following words are spelled[^.!?]*[.!?]?`)

func stripWhisperPromptEcho(text string) string {
	if !whisperPromptEchoRE.MatchString(text) {
		return text
	}
	return strings.Join(strings.Fields(whisperPromptEchoRE.ReplaceAllString(text, " ")), " ")
}
