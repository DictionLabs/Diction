package core

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHasDegenerateRepetition(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"normal sentence", "the quick brown fox jumps over the lazy dog", false},
		{"short plausible repeat", "no no no no no way", false},
		{"exact threshold run", strings.Repeat("tamb ", degenerateRepetitionThreshold), true},
		{"just under threshold", strings.Repeat("tamb ", degenerateRepetitionThreshold-1), false},
		{"mixed case and punctuation still collapses", strings.Repeat("Tamb, ", degenerateRepetitionThreshold), true},
		{"long text without a run", strings.Repeat("alpha beta gamma delta ", 10), false},
		{"run broken up short of threshold", strings.Repeat("tamb tamb tamb foo ", 5), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDegenerateRepetition(c.text); got != c.want {
				t.Errorf("hasDegenerateRepetition(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

// TestTranscriptionHandler_Hallucination_RetriesOnFallback verifies the REST
// path treats a degenerate-repetition transcript the same as a backend 5xx:
// primary is marked unhealthy and the request is retried on the fallback
// backend, which returns a clean transcript to the client.
func TestTranscriptionHandler_Hallucination_RetriesOnFallback(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"%s"}`, strings.Repeat("tamb ", degenerateRepetitionThreshold))
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
	if body := rr.Body.String(); !bytes.Contains([]byte(body), []byte("fallback ok")) {
		t.Errorf("body: want fallback response, got %q", body)
	}
}
