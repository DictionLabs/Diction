package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// handleTextProcess handles POST /v1/text/process.
// Wire contract (R11): plaintext JSON only, no E2E. Bearer token optional.
// When LLM is not configured, returns 503 with {"error":"llm_not_configured"}.
func handleTextProcess(llm llmConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

		if !llm.Enabled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"llm_not_configured"}`)) //nolint:errcheck
			return
		}

		// Reject E2E requests -- this endpoint is plaintext only (R11).
		// The Diction app only sends E2E to diction.cloud; a self-hoster's
		// gateway will never see X-Diction-E2E in normal operation.
		if r.Header.Get("X-Diction-E2E") != "" {
			http.Error(w, `{"error":"e2e not supported on this gateway"}`, http.StatusBadRequest)
			return
		}

		var body struct {
			Text    string `json:"text"`
			Context string `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
			http.Error(w, `{"error":"text field required"}`, http.StatusBadRequest)
			return
		}

		intent := r.URL.Query().Get("intent")

		timeoutSecs := 30 + len(body.Text)/400
		if timeoutSecs > 120 {
			timeoutSecs = 120
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSecs)*time.Second)
		defer cancel()

		resultText, err := llm.processWithIntent(ctx, body.Text, body.Context, intent)
		if err != nil {
			// edit-selected: return status:"failed" + original selection
			if intent == "edit-selected" {
				var tc struct {
					Selected string `json:"selected"`
				}
				json.Unmarshal([]byte(body.Context), &tc) //nolint:errcheck
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
					"text":   tc.Selected,
					"mode":   "edit-selected",
					"status": "failed",
				})
				return
			}
			if intent == "edit" {
				http.Error(w, `{"error":"processing failed"}`, http.StatusInternalServerError)
				return
			}
			// transcribe/cleanup: ship raw text
			resultText = body.Text
		}

		mode := intent
		if mode == "" || mode == "transcribe" {
			mode = "transcribe"
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"text": resultText,
			"mode": mode,
		})
	}
}

// handleTextSuggest handles POST /v1/text/suggest.
// Wire contract (R11): plaintext JSON only, no E2E. Bearer token optional.
// Returns {"suggestions":[]} on any error (soft-fail).
func handleTextSuggest(llm llmConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

		w.Header().Set("Content-Type", "application/json")

		if !llm.Enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"llm_not_configured"}`)) //nolint:errcheck
			return
		}

		if r.Header.Get("X-Diction-E2E") != "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"e2e not supported on this gateway"}`)) //nolint:errcheck
			return
		}

		var body struct {
			Selected    string   `json:"selected"`
			Before      string   `json:"before"`
			After       string   `json:"after"`
			CustomWords []string `json:"customWords"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			json.NewEncoder(w).Encode(map[string][]string{"suggestions": {}}) //nolint:errcheck
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		suggestions, _ := llm.suggestFixes(ctx, body.Selected, body.Before, body.After, body.CustomWords)
		json.NewEncoder(w).Encode(map[string]any{"suggestions": suggestions}) //nolint:errcheck
	}
}

// summaryMinWords is the threshold below which the summary pass is skipped.
// A short note is its own summary.
const summaryMinWords = 50

// summarizeMaxBytes caps the request body. Pre-cleaned voice notes top out
// around 1-2 KB; 8 KB gives headroom for long sessions without exposing the
// LLM to runaway inputs.
const summarizeMaxBytes = 8 * 1024

// handleTextSummarize handles POST /v1/text/summarize.
//
// Body:     {"text": "<note>", "language": "en|de|.../auto"}
// Response: {"summary": "..."|null, "summary_status": "succeeded|too_short|failed"}
//
// Whole-note operation: no cursor, selection or surrounding context, because a
// saved note has none. Wire contract matches the other text routes (R11):
// plaintext JSON only, bearer optional, same fail-closed middleware.
//
// Failure is soft. A missing summary costs the user a nicer History row and
// nothing else, so a dead LLM must not turn into a visible error; the caller
// records the status and retries later.
func handleTextSummarize(llm llmConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, summarizeMaxBytes+1)

		if !llm.Enabled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"llm_not_configured"}`)) //nolint:errcheck
			return
		}

		if r.Header.Get("X-Diction-E2E") != "" {
			http.Error(w, `{"error":"e2e not supported on this gateway"}`, http.StatusBadRequest)
			return
		}

		var body struct {
			Text     string `json:"text"`
			Language string `json:"language"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			// http.MaxBytesError is the precise type the reader returns when the
			// body exceeds the cap. Type-assert rather than string-match so this
			// survives stdlib message changes.
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		text := strings.TrimSpace(body.Text)
		if text == "" {
			http.Error(w, `{"error":"text field required"}`, http.StatusBadRequest)
			return
		}
		if len(text) > summarizeMaxBytes {
			http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}

		resp := map[string]any{"summary": nil, "summary_status": "too_short"}

		if wordCount(text) >= summaryMinWords {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			summary, err := llm.summarise(ctx, text, body.Language)
			cancel()
			if err != nil {
				log.Printf("text_summarize failed: %v", err)
				resp["summary_status"] = "failed"
			} else {
				resp["summary"] = summary
				resp["summary_status"] = "succeeded"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

// wordCount counts whitespace-separated tokens. CJK scripts that do not separate
// words with whitespace under-count, which is the right bias: they pack more
// meaning per character, so the threshold should fire on shorter strings there.
func wordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if inWord {
				n++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		n++
	}
	return n
}
