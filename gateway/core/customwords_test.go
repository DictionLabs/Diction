package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// STT custom-word boost: the custom_words field reaches backends with
// AcceptsCustomWords (Cohere, Parakeet) and nothing else. Plan:
// .claude/plans/cohere-custom-word-boost-plan.md.

const cwContext = `{"customWords":[{"word":"Mochala"},{"word":"Endře"}]}`

func TestBuildCustomWordsField(t *testing.T) {
	cases := []struct{ name, ctx, want string }{
		{"empty", "", ""},
		{"invalid", "{not json", ""},
		{"no words", `{"before":"x"}`, ""},
		{"empty words skipped", `{"customWords":[{"word":""},{"word":"A"}]}`, `["A"]`},
		{"order and unicode kept", cwContext, `["Mochala","Endře"]`},
		{"quotes and commas escaped", `{"customWords":[{"word":"a \"b\", c"}]}`, `["a \"b\", c"]`},
	}
	for _, c := range cases {
		if got := buildCustomWordsField(c.ctx); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestBuildCustomWordsField_CappedInClientOrder(t *testing.T) {
	var words []string
	var parts []string
	for i := 0; i < maxCustomWordsField+20; i++ {
		w := fmt.Sprintf("w%03d", i)
		words = append(words, w)
		parts = append(parts, fmt.Sprintf(`{"word":%q}`, w))
	}
	got := buildCustomWordsField(`{"customWords":[` + strings.Join(parts, ",") + `]}`)
	var decoded []string
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(decoded) != maxCustomWordsField || decoded[0] != words[0] || decoded[len(decoded)-1] != words[maxCustomWordsField-1] {
		t.Errorf("cap/order wrong: len=%d first=%q last=%q", len(decoded), decoded[0], decoded[len(decoded)-1])
	}
}

func TestCustomWordsFor_OnlyFlaggedBackends(t *testing.T) {
	if customWordsFor(nil, `["A"]`) != "" {
		t.Error("nil backend got the field")
	}
	if customWordsFor(&Backend{Name: "large-v3-turbo"}, `["A"]`) != "" {
		t.Error("unflagged backend got the field")
	}
	if customWordsFor(&Backend{Name: "cohere-transcribe", AcceptsCustomWords: true}, `["A"]`) != `["A"]` {
		t.Error("flagged backend did not get the field")
	}
}

func TestDefaultBackends_OnlyCohereParakeetAndCanaryAcceptCustomWords(t *testing.T) {
	// Parakeet added 2026-10-18: .claude/plans/parakeet-custom-word-boost-plan.md.
	// Canary added 2026-10-18: .claude/plans/canary-custom-word-boost-plan.md.
	want := map[string]bool{"cohere-transcribe": true, "parakeet-v3": true, "canary-v2": true}
	for _, b := range DefaultBackends() {
		if b.AcceptsCustomWords != want[b.Name] {
			t.Errorf("%s: AcceptsCustomWords=%v", b.Name, b.AcceptsCustomWords)
		}
	}
}

// formFields reads every non-file field of a multipart body.
func formFields(t *testing.T, body []byte, contentType string) map[string][]string {
	t.Helper()
	_, params, _ := parseMediaType(contentType)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	out := map[string][]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() != "file" {
			data, _ := io.ReadAll(part)
			out[part.FormName()] = append(out[part.FormName()], string(data))
		}
		part.Close()
	}
}

func TestRewriteMultipart_CustomWords(t *testing.T) {
	// A client-sent custom_words is replaced when the gateway writes its own, and passes
	// through untouched otherwise (pre-existing behaviour for every other backend).
	body, ct := buildMultipart(t, map[string]string{"language": "en", "custom_words": `["injected"]`}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	out, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{CustomWordsJSON: `["Mochala"]`})
	if err != nil {
		t.Fatal(err)
	}
	if got := formFields(t, out, newCT)["custom_words"]; len(got) != 1 || got[0] != `["Mochala"]` {
		t.Errorf("with field: got %v", got)
	}

	out, newCT, _, err = rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := formFields(t, out, newCT)["custom_words"]; len(got) != 1 || got[0] != `["injected"]` {
		t.Errorf("without field: client value should pass through unchanged, got %v", got)
	}
}

// fieldRecorder is a backend that records the custom_words and prompt it received.
type fieldRecorder struct {
	mu          sync.Mutex
	customWords []string
	prompts     []string
}

func (f *fieldRecorder) server(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse failed", 400)
			return
		}
		f.mu.Lock()
		f.customWords = append(f.customWords, r.FormValue("custom_words"))
		f.prompts = append(f.prompts, r.FormValue("prompt"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			fmt.Fprint(w, `{"text":"ok"}`)
		} else {
			fmt.Fprint(w, `{"error":"down"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fieldRecorder) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.customWords...), append([]string(nil), f.prompts...)
}

func postTranscription(t *testing.T, g *Gateway) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := buildMultipart(t, map[string]string{"context": cwContext, "language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	return rr
}

func TestTranscriptionHandler_CustomWordsOnlyToFlaggedBackend(t *testing.T) {
	wantPrompt := buildWhisperPrompt(cwContext)
	for _, flagged := range []bool{true, false} {
		rec := &fieldRecorder{}
		srv := rec.server(t, http.StatusOK)
		g := &Gateway{
			backends:     []Backend{{Name: "b", URL: srv.URL, Aliases: []string{"b"}, AcceptsCustomWords: flagged}},
			health:       newHealthState(),
			defaultModel: "b",
			maxBodySize:  10 * 1024 * 1024,
		}
		if rr := postTranscription(t, g); rr.Code != http.StatusOK {
			t.Fatalf("flagged=%v: status %d", flagged, rr.Code)
		}
		cw, prompts := rec.snapshot()
		want := ""
		if flagged {
			want = `["Mochala","Endře"]`
		}
		if len(cw) != 1 || cw[0] != want {
			t.Errorf("flagged=%v: custom_words got %v want %q", flagged, cw, want)
		}
		if len(prompts) != 1 || prompts[0] != wantPrompt {
			t.Errorf("flagged=%v: prompt changed: %v", flagged, prompts)
		}
	}
}

func TestTranscriptionHandler_RetryToUnflaggedBackendDropsCustomWords(t *testing.T) {
	primaryRec, fallbackRec := &fieldRecorder{}, &fieldRecorder{}
	primary := primaryRec.server(t, http.StatusInternalServerError)
	fallback := fallbackRec.server(t, http.StatusOK)
	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}, AcceptsCustomWords: true},
			{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("fallback", true)

	if rr := postTranscription(t, g); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if cw, _ := primaryRec.snapshot(); len(cw) != 1 || cw[0] == "" {
		t.Errorf("primary should have received custom_words, got %v", cw)
	}
	if cw, _ := fallbackRec.snapshot(); len(cw) != 1 || cw[0] != "" {
		t.Errorf("fallback must not receive custom_words, got %v", cw)
	}
}

func TestProxyToBackend_CustomWordsOnlyToFlaggedBackend(t *testing.T) {
	for _, flagged := range []bool{true, false} {
		rec := &fieldRecorder{}
		srv := rec.server(t, http.StatusOK)
		target, _ := url.Parse(srv.URL)
		_, err := testGateway().proxyToBackend(context.Background(), target,
			audioPayload{data: make([]byte, 100), filename: "audio.wav"},
			&Backend{Name: "b", AcceptsCustomWords: flagged}, "en", "", `["Mochala"]`)
		if err != nil {
			t.Fatal(err)
		}
		cw, _ := rec.snapshot()
		want := ""
		if flagged {
			want = `["Mochala"]`
		}
		if len(cw) != 1 || cw[0] != want {
			t.Errorf("flagged=%v: got %v want %q", flagged, cw, want)
		}
	}
}

// End to end on /v1/audio/stream: context frame → custom_words on the flagged
// primary, dropped on the retry to an unflagged fallback.
func TestStreamHandler_CustomWordsAndRetry(t *testing.T) {
	primaryRec, fallbackRec := &fieldRecorder{}, &fieldRecorder{}
	primary := primaryRec.server(t, http.StatusInternalServerError)
	fallback := fallbackRec.server(t, http.StatusOK)
	srv, _ := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}, AcceptsCustomWords: true},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=en"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(cwContext)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if cw, _ := primaryRec.snapshot(); len(cw) != 1 || cw[0] != `["Mochala","Endře"]` {
		t.Errorf("primary custom_words: %v", cw)
	}
	if cw, _ := fallbackRec.snapshot(); len(cw) != 1 || cw[0] != "" {
		t.Errorf("fallback must not receive custom_words: %v", cw)
	}
}
