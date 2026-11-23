package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func hallucinationGateway(t *testing.T, primaryURL, fallbackURL string) *Gateway {
	t.Helper()
	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primaryURL, Aliases: []string{"primary"}},
			{Name: "fallback", URL: fallbackURL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
		demotionTTL:   120 * time.Second,
	}
	useFakeClock(g)
	probeResult(g, "primary", true)
	probeResult(g, "fallback", true)
	return g
}

func postHallucinationRequest(t *testing.T, g *Gateway) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.wav", "RIFFfake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	return rr
}

var loopingTranscript = strings.Repeat("the the ", 40)

func TestTranscriptionHandler_HallucinationDoesNotDemoteBackend(t *testing.T) {
	primary, primaryHits := countingBackend(t, http.StatusOK, loopingTranscript)
	fallback, fallbackHits := countingBackend(t, http.StatusOK, loopingTranscript)
	g := hallucinationGateway(t, primary.URL, fallback.URL)
	failed, restoreFailed := withCapturedOnRequestFailed(t)
	events, restoreErr := withCapturedOnError(t)

	rr := postHallucinationRequest(t, g)

	if rr.Code < 500 {
		t.Errorf("client must get an error when every attempt hallucinates, got %d", rr.Code)
	}
	if !g.health.get("primary") || !g.health.get("fallback") {
		t.Error("a hallucination-shaped transcript must not demote a shared backend")
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 1 {
		t.Errorf("hits primary=%d fallback=%d; want exactly one rescue attempt", primaryHits.Load(), fallbackHits.Load())
	}
	restoreFailed()
	restoreErr()
	if n := failed.Len(); n != 1 {
		t.Errorf("OnRequestFailed fired %d times, want 1", n)
	}
	for _, e := range events.All() {
		if e.Kind == "stt_backend_5xx" {
			t.Errorf("hallucination must not be reported as a demoting backend 5xx: %+v", e)
		}
	}
}

func TestTranscriptionHandler_Hallucination_RetriesOnFallbackWithoutDemotion(t *testing.T) {
	primary, primaryHits := countingBackend(t, http.StatusOK, loopingTranscript)
	fallback, fallbackHits := countingBackend(t, http.StatusOK, "fallback ok")
	g := hallucinationGateway(t, primary.URL, fallback.URL)
	failed, restoreFailed := withCapturedOnRequestFailed(t)

	rr := postHallucinationRequest(t, g)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "fallback ok") {
		t.Fatalf("want the fallback's transcript, got %d %q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Diction-Route-Retry") != "true" {
		t.Error("retry response must carry X-Diction-Route-Retry")
	}
	if !g.health.get("primary") {
		t.Error("the primary must stay healthy after a hallucination")
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 1 {
		t.Errorf("hits primary=%d fallback=%d; want 1/1", primaryHits.Load(), fallbackHits.Load())
	}
	restoreFailed()
	if n := failed.Len(); n != 0 {
		t.Errorf("a rescued request must not count as failed, OnRequestFailed fired %d times", n)
	}
}

func TestTranscriptionHandler_Hallucination_NoAlternativeFailsWithoutDemotion(t *testing.T) {
	primary, primaryHits := countingBackend(t, http.StatusOK, loopingTranscript)
	g := &Gateway{
		backends:     []Backend{{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}}},
		health:       newHealthState(),
		defaultModel: "primary",
		maxBodySize:  10 * 1024 * 1024,
		demotionTTL:  120 * time.Second,
	}
	useFakeClock(g)
	probeResult(g, "primary", true)

	rr := postHallucinationRequest(t, g)

	if rr.Code < 500 {
		t.Errorf("want an error, got %d", rr.Code)
	}
	if !g.health.get("primary") {
		t.Error("the only backend must stay healthy")
	}
	if primaryHits.Load() != 1 {
		t.Errorf("primary hits = %d, want 1", primaryHits.Load())
	}
}
