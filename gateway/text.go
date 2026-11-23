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

// /v1/text/process budget (ms): base + per 1,000 UTF-8 bytes, capped; advertised on /v1/models.
const (
	textProcessBaseMs         = 30000
	textProcessPer1000BytesMs = 2500 // 1 s per 400 bytes
	textProcessMaxMs          = 120000
)

// textProcessBudget is the time a text of n bytes gets on /v1/text/process.
func textProcessBudget(n int) time.Duration {
	ms := textProcessBaseMs + n*textProcessPer1000BytesMs/1000
	if n > textProcessMaxMs || ms > textProcessMaxMs { // first check guards n*2500 overflow on 32-bit
		ms = textProcessMaxMs
	}
	return time.Duration(ms) * time.Millisecond
}

// handleTextProcess handles POST /v1/text/process: plaintext JSON only (R11), 503 without an LLM.
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
			writeBody(w, `{"error":"llm_not_configured"}`)
			return
		}

		// Plaintext only (R11): reject E2E requests.
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

		ctx, cancel := context.WithTimeout(r.Context(), textProcessBudget(len(body.Text)))
		defer cancel()

		resultText, err := llm.processWithIntent(ctx, body.Text, body.Context, intent)
		if err != nil {
			// edit-selected: return status:"failed" + original selection
			if intent == editSelectedIntent {
				var tc struct {
					Selected string `json:"selected"`
				}
				_ = json.Unmarshal([]byte(body.Context), &tc)
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, map[string]string{
					"text":   tc.Selected,
					"mode":   editSelectedIntent,
					"status": "failed",
				})
				return
			}
			if intent == editIntent {
				http.Error(w, `{"error":"processing failed"}`, http.StatusInternalServerError)
				return
			}
			// transcribe/cleanup: ship raw text
			resultText = body.Text
		}

		mode := modeForIntent(intent)

		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]string{
			"text": resultText,
			"mode": mode,
		})
	}
}

// writeBody writes s, discarding the error: the client is gone, nothing to recover.
func writeBody(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(s))
}

// writeJSON encodes v to w, discarding the error: the client is gone, nothing to recover.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// handleTextSuggest handles POST /v1/text/suggest: plaintext only (R11), soft-fails to [].
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
			writeBody(w, `{"error":"llm_not_configured"}`)
			return
		}

		if r.Header.Get("X-Diction-E2E") != "" {
			w.WriteHeader(http.StatusBadRequest)
			writeBody(w, `{"error":"e2e not supported on this gateway"}`)
			return
		}

		var body struct {
			Selected    string   `json:"selected"`
			Before      string   `json:"before"`
			After       string   `json:"after"`
			CustomWords []string `json:"customWords"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, map[string][]string{"suggestions": {}})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		suggestions := llm.suggestFixes(ctx, body.Selected, body.Before, body.After, body.CustomWords)
		writeJSON(w, map[string]any{"suggestions": suggestions})
	}
}

// summaryMinWords: below this a note is its own summary and the pass is skipped.
const summaryMinWords = 50

// summarizeMaxBytes caps the body: notes run 1-2 KB, 8 KB leaves headroom without runaway input.
const summarizeMaxBytes = 8 * 1024

// handleTextSummarize handles POST /v1/text/summarize; failure is soft (status field, no error).
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
			writeBody(w, `{"error":"llm_not_configured"}`)
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
			// Match the reader's MaxBytesError type, not its message.
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

		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, summarizeResponse(r.Context(), llm, text, body.Language))
	}
}

// summarizeResponse builds the reply; a failed or skipped summary is a status, not an error.
func summarizeResponse(ctx context.Context, llm llmConfig, text, language string) map[string]any {
	resp := map[string]any{"summary": nil, "summary_status": "too_short"}
	if wordCount(text) < summaryMinWords {
		return resp
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	summary, err := llm.summarise(ctx, text, language)
	cancel()
	if err != nil {
		log.Printf("text_summarize failed: %v", err)
		resp["summary_status"] = "failed"
		return resp
	}
	resp["summary"] = summary
	resp["summary_status"] = "succeeded"
	return resp
}

// wordCount counts whitespace-separated tokens; CJK under-counts, the right bias (denser text).
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
