package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTranscriptionHandler_RetriesOn5xx_SucceedsOnFallback(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"primary down"}`)
	}))
	defer primary.Close()

	fallbackHits := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestTranscriptionHandler_RetriesOn5xx_FallbackAlsoFails(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"primary down"}`)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502 (retry backend), got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Diction-Route-Retry"); got != "true" {
		t.Errorf("X-Diction-Route-Retry: want true, got %q", got)
	}
}

func TestTranscriptionHandler_RetryStripsAutoDetectLanguage(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
