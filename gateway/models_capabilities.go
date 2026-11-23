package main

import (
	"encoding/json"
	"net/http"
)

// capabilityFlags carries the advertised feature set into withCapabilities.
type capabilityFlags struct {
	llmEnabled  bool
	textRoutes  bool
	pairing     bool
	keyRotation bool
	// postDeliveryMs: DICTION_LIVE_ENHANCE_TIMEOUT_MS, advertised so the app waits as long.
	postDeliveryMs int
	// preview / previewOpus: preview=cloud interim frames on PCM / Opus sessions.
	preview     bool
	previewOpus bool
	// chat: POST /v1/chat has a backend and this caller can reach it.
	chat bool
}

// withCapabilities adds a top-level "capabilities" object to /v1/models; data[] stays untouched.
func withCapabilities(base http.HandlerFunc, flagsFor func(*http.Request) capabilityFlags) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flags := flagsFor(r)
		rec := &responseRecorder{header: make(http.Header), code: http.StatusOK}
		base(rec, r)

		// Non-200, non-JSON or unparseable: pass through unchanged.
		var baseResp map[string]json.RawMessage
		contentType := rec.header.Get("Content-Type")
		if rec.code != http.StatusOK || contentType == "" ||
			json.Unmarshal(rec.body, &baseResp) != nil {
			copyHeaders(w, rec)
			_, _ = w.Write(rec.body)
			return
		}

		baseResp["capabilities"] = capabilitiesJSON(flags)
		// Numbers do not fit the bool-only map; absent without an LLM (nothing to wait for).
		if flags.llmEnabled {
			baseResp["writing_tools"] = writingToolsJSON(flags)
		}

		copyHeaders(w, rec)
		_ = json.NewEncoder(w).Encode(baseResp)
	}
}

// copyHeaders replays the recorded headers and status code onto w.
func copyHeaders(w http.ResponseWriter, rec *responseRecorder) {
	for k, vv := range rec.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.code)
}

func capabilitiesJSON(flags capabilityFlags) json.RawMessage {
	caps, _ := json.Marshal(map[string]bool{
		"llm":            flags.llmEnabled,
		"text_process":   flags.textRoutes,
		"text_suggest":   flags.textRoutes,
		"text_summarize": flags.textRoutes,
		// Advertised separately so the Formatting toggle shows only where the key is honoured.
		"formatting": flags.llmEnabled,
		// key_rotation is false when the key is pinned via DICTION_GATEWAY_KEY.
		"pairing":      flags.pairing,
		"key_rotation": flags.keyRotation,
		// preview_opus alone is read as not capable: it only means anything with preview.
		"preview":      flags.preview,
		"preview_opus": flags.previewOpus,
		"chat":         flags.chat,
	})
	return caps
}

// writingToolsJSON advertises the text/process budget so the app's deadline follows.
func writingToolsJSON(flags capabilityFlags) json.RawMessage {
	wt, _ := json.Marshal(map[string]int{
		"post_delivery_ms":          flags.postDeliveryMs,
		"process_base_ms":           textProcessBaseMs,
		"process_per_1000_bytes_ms": textProcessPer1000BytesMs,
		"process_max_ms":            textProcessMaxMs,
	})
	return wt
}

// responseRecorder captures an http.Handler's response.
type responseRecorder struct {
	header http.Header
	code   int
	body   []byte
}

func (r *responseRecorder) Header() http.Header  { return r.header }
func (r *responseRecorder) WriteHeader(code int) { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}
