package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTranscriptionHandler_RetriesOn5xx_SucceedsOnFallback verifies the
// primary-500 → mark unhealthy → re-route via ModelForLanguage → retry on the
// fallback backend path (proxy.go:617-702). Setup: two backends where the
// primary returns 500 and the fallback returns 200; the client sees 200 and
// the response is tagged with the retry headers.
func TestTranscriptionHandler_RetriesOn5xx_SucceedsOnFallback(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"primary down"}`)
	}))
	defer primary.Close()

	fallbackHits := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"fallback ok"}`)
	}))
	defer fallback.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
			{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("fallback", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200 after retry, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if primaryHits != 1 {
		t.Errorf("primary hits: want 1, got %d", primaryHits)
	}
	if fallbackHits != 1 {
		t.Errorf("fallback hits: want 1, got %d", fallbackHits)
	}
	if got := rr.Header().Get("X-Diction-Route-Retry"); got != "true" {
		t.Errorf("X-Diction-Route-Retry: want true, got %q", got)
	}
	if body := rr.Body.String(); !bytes.Contains([]byte(body), []byte("fallback ok")) {
		t.Errorf("body: want fallback response, got %q", body)
	}
}

// TestTranscriptionHandler_RetriesOn5xx_FallbackAlsoFails verifies the
// "retry also 5xx" branch (proxy.go:695-699): both backends 500, client
// sees the retry backend's 500 (not the primary's), and the retry is
// still tagged so telemetry can attribute the failure to the fallback.
func TestTranscriptionHandler_RetriesOn5xx_FallbackAlsoFails(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"primary down"}`)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":"fallback also down"}`)
	}))
	defer fallback.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
			{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("fallback", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	// Client sees the retry backend's status, not the primary's.
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502 (retry backend), got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Diction-Route-Retry"); got != "true" {
		t.Errorf("X-Diction-Route-Retry: want true, got %q", got)
	}
}

// TestTranscriptionHandler_RetryStripsAutoDetectLanguage verifies the B2 fix
// (.claude/bow/stt-failure-rate-2026-09-13.md, plans/stt-failure-visibility-plan.md):
// the HTTP retry path must apply the same StripLanguage/LanguageOverride
// decision the first attempt made, not forward the client's raw
// language=auto sentinel. Setup: cold auto-detect (no profile) routes the
// first attempt to the fallback model with the language field stripped; the
// fallback 500s; the retry re-routes to the default model. Before this fix,
// the retry's rewriteMultipart call carried neither StripLanguage nor
// LanguageOverride, so the literal "auto" sentinel — accepted by no backend
// — reached the retry backend verbatim.
func TestTranscriptionHandler_RetryStripsAutoDetectLanguage(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"primary down"}`)
	}))
	defer primary.Close()

	var retryLanguage string
	var retryLanguagePresent bool
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		boundary := parseBoundary(r.Header.Get("Content-Type"))
		retryLanguagePresent = presentFields(t, b, r.Header.Get("Content-Type"))["language"]
		retryLanguage = extractFormField(b, boundary, "language")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"secondary ok"}`)
	}))
	defer secondary.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
			{Name: "secondary", URL: secondary.URL, Aliases: []string{"secondary"}},
		},
		health:        newHealthState(),
		defaultModel:  "secondary",
		fallbackModel: "primary",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("secondary", true)

	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200 after retry, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if retryLanguagePresent {
		t.Errorf("retry backend received language=%q; the literal auto-detect sentinel must be stripped, not forwarded", retryLanguage)
	}
}
