package main

import (
	"encoding/json"
	"net/http"
)

// withCapabilities wraps a ModelsHandler to inject a top-level "capabilities"
// object into the /v1/models JSON response (R4, R18).
//
// Shape: {"capabilities":{"llm":bool,"text_process":bool,"text_suggest":bool}}.
// Additive: existing data[] and providers[] are untouched, so every existing
// client and the shipped GatewayProbe keep working without changes.
//
// core/ is not modified -- the decorator lives in main, so the two byte-identical
// hand-maintained copies of core/ across both git roots stay untouched (R18).
func withCapabilities(base http.HandlerFunc, llmEnabled, routesOpen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Capture the base handler's output.
		rec := &responseRecorder{header: make(http.Header), code: http.StatusOK}
		base(rec, r)

		// Pass through non-200 or non-JSON unchanged.
		contentType := rec.header.Get("Content-Type")
		if rec.code != http.StatusOK || contentType == "" {
			for k, vv := range rec.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.code)
			w.Write(rec.body) //nolint:errcheck
			return
		}

		// Parse the base response.
		var baseResp map[string]json.RawMessage
		if err := json.Unmarshal(rec.body, &baseResp); err != nil {
			// Unparseable: pass through unchanged.
			for k, vv := range rec.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.code)
			w.Write(rec.body) //nolint:errcheck
			return
		}

		// Inject capabilities.
		textRoutes := llmEnabled && routesOpen
		caps, _ := json.Marshal(map[string]bool{
			"llm":          llmEnabled,
			"text_process": textRoutes,
			"text_suggest": textRoutes,
		})
		baseResp["capabilities"] = caps

		// Write augmented response.
		for k, vv := range rec.header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.code)
		json.NewEncoder(w).Encode(baseResp) //nolint:errcheck
	}
}

// responseRecorder captures an http.Handler's response.
type responseRecorder struct {
	header http.Header
	code   int
	body   []byte
}

func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) WriteHeader(code int)        { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error) { r.body = append(r.body, b...); return len(b), nil }
