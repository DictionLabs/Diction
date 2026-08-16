package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
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
