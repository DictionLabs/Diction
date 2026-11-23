package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractFormField_Found(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"context": `{"before":"hello","after":"world"}`, "language": "en"}, "audio.wav", "data")
	boundary := parseBoundary(ct)
	got := extractFormField(body, boundary, "context")
	if got != `{"before":"hello","after":"world"}` {
		t.Errorf("context: want JSON, got %q", got)
	}
}

func TestExtractFormField_NotFound(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.wav", "data")
	boundary := parseBoundary(ct)
	got := extractFormField(body, boundary, "context")
	if got != "" {
		t.Errorf("context: want empty, got %q", got)
	}
}

func TestBuildWhisperPrompt_EmptyContext(t *testing.T) {
	if got := buildWhisperPrompt(""); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestBuildWhisperPrompt_NoCustomWords(t *testing.T) {
	got := buildWhisperPrompt(`{"before":"hello","after":"world"}`)
	if got != "" {
		t.Errorf("want empty when no customWords, got %q", got)
	}
}

func TestBuildWhisperPrompt_SingleWord(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":"Kubernetes"}]}`)
	want := "The following words are spelled exactly as shown: Kubernetes."
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBuildWhisperPrompt_MultipleWords(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":"PostgreSQL"},{"word":"WebSocket"},{"word":"OAuth"}]}`)
	want := "The following words are spelled exactly as shown: PostgreSQL, WebSocket, OAuth."
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBuildWhisperPrompt_WithVariants(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":"Kubernetes","variants":["cube er netties"]},{"word":"PostgreSQL","variants":["post gres sequel"]}]}`)
	want := "The following words are spelled exactly as shown: Kubernetes, PostgreSQL."
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBuildWhisperPrompt_InvalidJSON(t *testing.T) {
	got := buildWhisperPrompt(`not-json`)
	if got != "" {
		t.Errorf("want empty on invalid JSON, got %q", got)
	}
}

func TestBuildWhisperPrompt_EmptyWordSkipped(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":""},{"word":"PostgreSQL"},{"word":""}]}`)
	want := "The following words are spelled exactly as shown: PostgreSQL."
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBuildWhisperPrompt_AllWordsEmpty(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":""},{"word":""}]}`)
	if got != "" {
		t.Errorf("want empty string when all words are blank, got %q", got)
	}
}

func TestTranscriptionHandler_WhisperPromptForwardedToBackend(t *testing.T) {
	var receivedPrompt string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse failed", 400)
			return
		}
		receivedPrompt = r.FormValue("prompt")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	contextJSON := `{"customWords":[{"word":"Kubernetes"},{"word":"PostgreSQL"}]}`
	body, ct := buildMultipart(t, map[string]string{"context": contextJSON, "language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	wantPrompt := "The following words are spelled exactly as shown: Kubernetes, PostgreSQL."
	if receivedPrompt != wantPrompt {
		t.Errorf("prompt forwarded to Whisper: want %q, got %q", wantPrompt, receivedPrompt)
	}
}

func TestStripWhisperPromptEcho(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The following words are spelled exactly as shown.", ""},
		{"The following words are spelled exactly as shown, Grafana, Xochitl, Xochitl", ""},
		{"The following words are spelled correctly. The following words are spelled correctly.", ""},
		{"The following words are spelled exactly as shown in the following words. Call Parker at three.", "Call Parker at three."},
		{"Call Parker at three.", "Call Parker at three."},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripWhisperPromptEcho(c.in); got != c.want {
			t.Errorf("stripWhisperPromptEcho(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTranscriptionHandler_PromptEchoStripped(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"The following words are spelled exactly as shown."}`)
	}))
	defer backend.Close()
	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: backend.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	body, ct := buildMultipart(t, map[string]string{"context": `{"customWords":[{"word":"Grafana"}]}`, "language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if got.Text != "" {
		t.Errorf("echoed prompt reached the client: %q", got.Text)
	}
}

func TestTranscriptionHandler_PromptEchoKeptWhenNoPromptSent(t *testing.T) {
	const said = "The following words are spelled exactly as shown."
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":%q}`, said)
	}))
	defer backend.Close()
	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: backend.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	if !strings.Contains(rr.Body.String(), said) {
		t.Errorf("user's own words were stripped: %q", rr.Body.String())
	}
}

func TestTranscriptionHandler_NoPromptWhenNoCustomWords(t *testing.T) {
	var receivedPrompt string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse failed", 400)
			return
		}
		receivedPrompt = r.FormValue("prompt")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	contextJSON := `{"before":"hello ","after":" world"}`
	body, ct := buildMultipart(t, map[string]string{"context": contextJSON, "language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if receivedPrompt != "" {
		t.Errorf("prompt should be empty when no customWords, got %q", receivedPrompt)
	}
}
