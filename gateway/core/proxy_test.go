package core

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildMultipart builds a multipart/form-data body with the given fields and an
// optional file part. Returns the body bytes and the content-type header value.
func buildMultipart(t *testing.T, fields map[string]string, fileName, fileContent string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField %s: %v", k, err)
		}
	}
	if fileName != "" {
		fw, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		fw.Write([]byte(fileContent))
	}
	w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

// --- parseBoundary ---

func TestParseBoundary_ExtractsBoundary(t *testing.T) {
	_, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	boundary := parseBoundary(ct)
	if boundary == "" {
		t.Error("expected non-empty boundary")
	}
}

func TestParseBoundary_InvalidContentType(t *testing.T) {
	boundary := parseBoundary("application/json")
	if boundary != "" {
		t.Errorf("boundary: want empty for non-multipart, got %s", boundary)
	}
}

func TestParseBoundary_ExplicitBoundary(t *testing.T) {
	boundary := parseBoundary("multipart/form-data; boundary=abc123")
	if boundary != "abc123" {
		t.Errorf("boundary: want abc123, got %s", boundary)
	}
}

// --- rewriteMultipart ---

func TestRewriteMultipart_StripsModelField(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"model": "medium", "language": "en"}, "audio.m4a", "fake-audio-data")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	// Parse the rewritten body and verify no model field
	_, params, _ := parseMediaType(newCT)
	newBoundary := params["boundary"]
	reader := multipart.NewReader(bytes.NewReader(rewritten), newBoundary)
	foundModel := false
	foundLanguage := false
	foundFile := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read rewritten part: %v", err)
		}
		switch part.FormName() {
		case "model":
			foundModel = true
		case "language":
			foundLanguage = true
		case "file":
			foundFile = true
		}
		part.Close()
	}

	if foundModel {
		t.Error("model field should have been stripped")
	}
	if !foundLanguage {
		t.Error("language field should be preserved")
	}
	if !foundFile {
		t.Error("file part should be preserved")
	}
}

func TestRewriteMultipart_PreservesFileContent(t *testing.T) {
	const audioContent = "this-is-fake-audio-bytes"
	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", audioContent)
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read rewritten part: %v", err)
		}
		if part.FormName() == "file" {
			data, _ := io.ReadAll(part)
			if string(data) != audioContent {
				t.Errorf("file content: want %q, got %q", audioContent, string(data))
			}
		}
		part.Close()
	}
}

func TestRewriteMultipart_NoModelField(t *testing.T) {
	// No model field - rewrite should still work cleanly
	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	_, _, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Helper to enumerate which form-field names survived a rewrite.
func presentFields(t *testing.T, body []byte, ct string) map[string]bool {
	t.Helper()
	_, params, _ := parseMediaType(ct)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	found := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		found[part.FormName()] = true
		part.Close()
	}
	return found
}

func TestRewriteMultipart_StripLanguageWhenAutoDetect(t *testing.T) {
	// When StripLanguage is set (auto-detect routing), `language` is dropped so the
	// upstream model runs native LID instead of being locked to the client's value.
	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		StripLanguage: true,
	})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	found := presentFields(t, rewritten, newCT)
	if found["language"] {
		t.Error("language must be stripped when StripLanguage=true")
	}
}

func TestRewriteMultipart_KeepsLanguageWhenSingleLang(t *testing.T) {
	// Default (StripLanguage=false): preserve the language field as-is.
	body, ct := buildMultipart(t, map[string]string{"language": "fr"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	found := presentFields(t, rewritten, newCT)
	if !found["language"] {
		t.Error("language must be preserved when StripLanguage=false")
	}
}

func TestRewriteMultipart_ConvertToWAV(t *testing.T) {
	// When convertToWAV=true with already-WAV input (RIFF passthrough), the file
	// part is re-emitted with Content-Type: audio/wav and filename "audio.wav".
	wavData := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", string(wavData))
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{ConvertToWAV: true})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "file" {
			if part.Header.Get("Content-Type") != "audio/wav" {
				t.Errorf("Content-Type: want audio/wav, got %s", part.Header.Get("Content-Type"))
			}
			if part.FileName() != "audio.wav" {
				t.Errorf("filename: want audio.wav, got %s", part.FileName())
			}
		}
		part.Close()
	}
}

// --- convertToWAVBytes ---

func TestConvertToWAVBytes_PassthroughWhenAlreadyWAV(t *testing.T) {
	// WAV files start with RIFF magic - should be returned unchanged.
	wavData := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	result, err := convertToWAVBytes(wavData, "audio.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, wavData) {
		t.Error("expected WAV data to be passed through unchanged")
	}
}

func TestConvertToWAVBytes_FFmpegErrorOnGarbage(t *testing.T) {
	// Non-RIFF garbage - exercises temp dir creation, file write, ffmpeg exec,
	// and the error-return path (ffmpeg can't decode random bytes as .m4a).
	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 64)
	_, err := convertToWAVBytes(garbage, "audio.m4a")
	if err == nil {
		t.Fatal("expected error when ffmpeg cannot decode garbage audio")
	}
}

func TestConvertToWAVBytes_EmptyFilename(t *testing.T) {
	// Empty filename → falls back to .m4a extension; ffmpeg still fails on garbage.
	garbage := bytes.Repeat([]byte{0xFF, 0xFE}, 32)
	_, err := convertToWAVBytes(garbage, "")
	if err == nil {
		t.Fatal("expected error for undecipherable audio without filename")
	}
}

// --- parseMediaType is a helper to avoid importing mime at the test level.
func parseMediaType(ct string) (string, map[string]string, error) {
	// simple parsing: split on ;
	parts := strings.SplitN(ct, ";", 2)
	params := make(map[string]string)
	if len(parts) == 2 {
		for _, p := range strings.Split(parts[1], ";") {
			p = strings.TrimSpace(p)
			kv := strings.SplitN(p, "=", 2)
			if len(kv) == 2 {
				params[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
			}
		}
	}
	return strings.TrimSpace(parts[0]), params, nil
}

// --- TranscriptionHandler ---

func TestTranscriptionHandler_MethodNotAllowed(t *testing.T) {
	g := testGateway()
	req := httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions", nil)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_NoMatchingBackend(t *testing.T) {
	// defaultModel has no matching backend - gateway returns 400
	g := &Gateway{
		backends:     []Backend{},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "data")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_BodyTooLarge(t *testing.T) {
	g := &Gateway{
		backends:     testGateway().backends,
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10, // tiny limit
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "this-is-way-too-long")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: want 413, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_FallbackModelRouteLogs(t *testing.T) {
	// When a fallbackModel is configured the gateway logs the route decision.
	// This test exercises the log.Printf branch (line 244) that is only reached
	// when g.fallbackModel != "".
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"routed"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "canary-v2", URL: backend.URL, Aliases: []string{"canary-v2"}},
			{Name: "large-v3-turbo", URL: backend.URL, Aliases: []string{"large-v3-turbo"}},
		},
		health:        newHealthState(),
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("canary-v2", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
}

func TestTranscriptionHandler_AutoDetectRoutesToFallback(t *testing.T) {
	// `language=auto` must route to the fallback model and strip the `language`
	// field from the upstream multipart body so native LID can run.
	var upstreamLanguage string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("backend: read multipart: %v", err)
		}
		for {
			part, perr := mr.NextPart()
			if perr != nil {
				break
			}
			if part.FormName() == "language" {
				b, _ := io.ReadAll(part)
				upstreamLanguage = string(b)
			}
			part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"detected"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "canary-v2", URL: backend.URL, Aliases: []string{"canary-v2"}},
			{Name: "large-v3-turbo", URL: backend.URL, Aliases: []string{"large-v3-turbo"}},
		},
		health:        newHealthState(),
		defaultModel:  "canary-v2",
		fallbackModel: "large-v3-turbo",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("canary-v2", true)
	g.health.set("large-v3-turbo", true)

	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if upstreamLanguage != "" {
		t.Errorf("language must be stripped upstream when auto-detect is active; got %q", upstreamLanguage)
	}
	if got := rr.Header().Get("X-Diction-Route-Model"); got != "large-v3-turbo" {
		t.Errorf("Route-Model: want large-v3-turbo, got %q", got)
	}
	if got := rr.Header().Get("X-Diction-Route-Detect"); got != "true" {
		t.Errorf("Route-Detect: want true, got %q", got)
	}
}

func TestTranscriptionHandler_OnTranscriptionCallback(t *testing.T) {
	// OnTranscription hook is called when the response is rewritten (enhance=true path).
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"callback test"}`)
	}))
	defer backend.Close()

	var gotModel string
	var gotChars int
	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
		OnTranscription: func(_ context.Context, model string, _ int64, chars int, _ int64, _, _ bool) {
			gotModel = model
			gotChars = chars
		},
	}
	g.health.set("small", true)

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(func(_ context.Context, text, _, _ string) (string, string, error) {
		return text, "transcribe", nil
	})(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
	if gotModel != "small" {
		t.Errorf("OnTranscription model: want small, got %q", gotModel)
	}
	if gotChars == 0 {
		t.Errorf("OnTranscription chars: want >0, got 0")
	}
}

func TestTranscriptionHandler_ProxiesToBackend(t *testing.T) {
	// Start a fake whisper backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello world"}`)
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

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hello world") {
		t.Errorf("expected transcription in body, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_WAVConversionFails(t *testing.T) {
	// Backend with NeedsWAV=true: gateway runs ffmpeg on the audio; garbage input
	// makes ffmpeg fail → rewriteMultipart returns error → handler returns 500.
	g := &Gateway{
		backends: []Backend{
			{Name: "parakeet-v3", URL: "http://parakeet:5092", Aliases: []string{"parakeet-v3"}, NeedsWAV: true},
		},
		health:       newHealthState(),
		defaultModel: "parakeet-v3",
		maxBodySize:  10 * 1024 * 1024,
	}

	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 64)
	body, ct := buildMultipart(t, map[string]string{"model": "parakeet-v3"}, "audio.m4a", string(garbage))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestTranscriptionHandler_WithPostProcess_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw transcript"}`)
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

	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		return "cleaned: " + text, "transcribe", nil
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cleaned: raw transcript") {
		t.Errorf("expected cleaned transcript, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_WithPostProcess_ErrorFallback(t *testing.T) {
	// postProcess error → raw transcript returned unchanged
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw transcript"}`)
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

	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		return "", "", fmt.Errorf("llm error")
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "raw transcript") {
		t.Errorf("expected raw transcript fallback, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_ModelStrippedBeforeProxy(t *testing.T) {
	// Verify model field is not forwarded to backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse failed", 400)
			return
		}
		if r.FormValue("model") != "" {
			http.Error(w, "model field should not be present", 400)
			return
		}
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

	body, ct := buildMultipart(t, map[string]string{"model": "small", "language": "en"}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

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

// --- buildWhisperPrompt ---

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
	if got != "Kubernetes" {
		t.Errorf("want %q, got %q", "Kubernetes", got)
	}
}

func TestBuildWhisperPrompt_MultipleWords(t *testing.T) {
	got := buildWhisperPrompt(`{"customWords":[{"word":"PostgreSQL"},{"word":"WebSocket"},{"word":"OAuth"}]}`)
	want := "PostgreSQL, WebSocket, OAuth"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestBuildWhisperPrompt_WithVariants(t *testing.T) {
	// Variants are LLM correction hints only — correct spelling is all Whisper gets
	got := buildWhisperPrompt(`{"customWords":[{"word":"Kubernetes","variants":["cube er netties"]},{"word":"PostgreSQL","variants":["post gres sequel"]}]}`)
	want := "Kubernetes, PostgreSQL"
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
	// Empty word strings within the list should be silently skipped
	got := buildWhisperPrompt(`{"customWords":[{"word":""},{"word":"PostgreSQL"},{"word":""}]}`)
	if got != "PostgreSQL" {
		t.Errorf("want %q, got %q", "PostgreSQL", got)
	}
}

func TestBuildWhisperPrompt_AllWordsEmpty(t *testing.T) {
	// customWords present but every Word field is "" — should return ""
	got := buildWhisperPrompt(`{"customWords":[{"word":""},{"word":""}]}`)
	if got != "" {
		t.Errorf("want empty string when all words are blank, got %q", got)
	}
}

// --- rewriteMultipart whisper prompt ---

func TestRewriteMultipart_InjectsWhisperPrompt(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{WhisperPrompt: "Kubernetes, PostgreSQL"})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	foundPrompt := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "prompt" {
			data, _ := io.ReadAll(part)
			foundPrompt = true
			if string(data) != "Kubernetes, PostgreSQL" {
				t.Errorf("prompt: want %q, got %q", "Kubernetes, PostgreSQL", string(data))
			}
		}
		part.Close()
	}
	if !foundPrompt {
		t.Error("expected prompt field in rewritten multipart")
	}
}

func TestRewriteMultipart_NoPromptWhenEmpty(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "prompt" {
			t.Error("prompt field should not be present when whisperPrompt is empty")
		}
		part.Close()
	}
}

// --- TranscriptionHandler whisper prompt forwarding ---

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
	if receivedPrompt != "Kubernetes, PostgreSQL" {
		t.Errorf("prompt forwarded to Whisper: want %q, got %q", "Kubernetes, PostgreSQL", receivedPrompt)
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

	// Context with no customWords — no prompt should be sent
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

func TestRewriteMultipart_StripsContextField(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"context": `{"before":"x"}`, "language": "en"}, "audio.wav", "audio")
	boundary := parseBoundary(ct)
	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}
	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "context" {
			t.Error("context field should have been stripped")
		}
		part.Close()
	}
}

func TestTranscriptionHandler_ContextFieldPassedToPostProcess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw"}`)
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

	var receivedContext string
	postProcess := func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		receivedContext = contextJSON
		return text, "", nil
	}

	contextJSON := `{"before":"hello ","after":" world","selected":"test"}`
	body, ct := buildMultipart(t, map[string]string{"context": contextJSON}, "audio.wav", "RIFF"+string(make([]byte, 100)))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if receivedContext != contextJSON {
		t.Errorf("context: want %q, got %q", contextJSON, receivedContext)
	}
}

func TestConvertToWAVBytes_SuccessfulConversion(t *testing.T) {
	// Generate a tiny silent MP3 via ffmpeg, then convert to WAV.
	// This exercises the full non-WAV path including os.ReadFile and return wavData, nil.
	tmpDir := t.TempDir()
	mp3Path := filepath.Join(tmpDir, "silence.mp3")
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono",
		"-t", "0.01", "-f", "mp3", mp3Path)
	gen.Stderr = io.Discard
	if err := gen.Run(); err != nil {
		t.Skipf("ffmpeg not available for conversion test: %v", err)
	}
	mp3Data, err := os.ReadFile(mp3Path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := convertToWAVBytes(mp3Data, "silence.mp3")
	if err != nil {
		t.Fatalf("convertToWAVBytes: %v", err)
	}
	if len(result) < 4 || string(result[:4]) != "RIFF" {
		t.Error("expected WAV output (RIFF header)")
	}
}

func TestTranscriptionHandler_E2EEncryptedResponse(t *testing.T) {
	// Verify that X-Diction-E2E header causes the response to be E2E encrypted.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello e2e world"}`)
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

	// Generate client ephemeral key
	clientPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", clientPubB64)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, ok := resp["e2e"]; !ok {
		t.Errorf("expected e2e key in response, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_E2EEncryptFails_ReturnsPlainText(t *testing.T) {
	// Bug: if EncryptTranscript fails (e.g. invalid client key), handler falls
	// through to plain {"text":"..."} instead of returning an error.
	// Client sent X-Diction-E2E expecting encrypted response but gets cleartext.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"secret transcript"}`)
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

	// 16 bytes is not a valid 32-byte X25519 public key — EncryptTranscript will fail
	badPub := base64.RawURLEncoding.EncodeToString(make([]byte, 16))

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", badPub)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Documents current behavior: E2E failure falls through to plain text.
	// This is a privacy concern — client expected encryption but got cleartext.
	if _, ok := resp["e2e"]; ok {
		t.Error("expected NO e2e key (encryption should have failed)")
	}
	if text, ok := resp["text"]; !ok || text != "secret transcript" {
		t.Errorf("expected plain text fallback, got: %s", rr.Body.String())
	}
}

func TestTranscriptionHandler_MalformedBackendURL_Returns400(t *testing.T) {
	// Backend exists but its URL is malformed — resolveBackend returns (nil, nil),
	// indistinguishable from "no matching model". Locks in the 400 response.
	g := &Gateway{
		backends:     []Backend{{Name: "broken", URL: "://\x00invalid", Aliases: []string{"broken"}}},
		health:       newHealthState(),
		defaultModel: "broken",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestTranscriptionHandler_NonJSONBackendResponse(t *testing.T) {
	// Backend returns non-JSON → falls through to plain response passthrough
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not-valid-json`)
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

	clientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "data")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", clientPubB64)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(nil)(rr, req)

	// Should pass through the original (non-JSON) body unchanged
	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
}

// --- response_format (OpenAI compatibility) ---

func TestResponseFormat_Text_ReturnsPlainText(t *testing.T) {
	// response_format=text: gateway unwraps the backend's {"text":"..."} and returns a plain body.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello plain world"}`)
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

	body, ct := buildMultipart(t, map[string]string{"response_format": "text"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type: want text/plain, got %q", got)
	}
	if got := rr.Body.String(); got != "hello plain world" {
		t.Errorf("body: want plain transcript, got %q", got)
	}
}

func TestResponseFormat_Json_Unchanged(t *testing.T) {
	// Default (no response_format) keeps the {"text":"..."} JSON shape.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"still json"}`)
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

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("default response must be JSON, got: %s", rr.Body.String())
	}
	if resp["text"] != "still json" {
		t.Errorf("text: want 'still json', got %v", resp["text"])
	}
}

func TestResponseFormat_UnsupportedRejected(t *testing.T) {
	// verbose_json/srt/vtt require word timestamps we don't produce — reject loud.
	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: "http://unreachable:0", Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"response_format": "verbose_json"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "verbose_json") {
		t.Errorf("error should name the unsupported format, got: %s", rr.Body.String())
	}
}

func TestResponseFormat_E2EConflict(t *testing.T) {
	// text body + E2E envelope are semantically incompatible — reject rather than silently
	// downgrade (both sides of the contract get violated otherwise).
	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: "http://unreachable:0", Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	clientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	body, ct := buildMultipart(t, map[string]string{"response_format": "text"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", clientPubB64)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestResponseFormat_BackendError_StaysJSON(t *testing.T) {
	// Backend 5xx must not get wrapped as plain text just because the client asked
	// for text output — OpenAI convention is that errors always come back as JSON,
	// and our writeTranscriptionResponse helper only runs on 2xx.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"backend down"}`)
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

	body, ct := buildMultipart(t, map[string]string{"response_format": "text"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500 (backend passthrough), got %d", rr.Code)
	}
	// Error body must remain JSON, not a plain-text rewrite.
	var errResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Errorf("backend error should stay JSON-shaped, got: %s", rr.Body.String())
	}
}

func TestRewriteMultipart_StripsResponseFormat(t *testing.T) {
	// response_format is a gateway-side control field; it must not be forwarded to the backend.
	body, ct := buildMultipart(t, map[string]string{"response_format": "text", "language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "response_format" {
			t.Error("response_format should have been stripped from multipart")
		}
		part.Close()
	}
}

func TestTranscriptionHandler_TargetPath(t *testing.T) {
	// Backends with TargetPath (e.g. canary-v2) must use that path instead of /v1/audio/transcriptions.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"hello","timestamps":null}`))
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "canary-v2", URL: backend.URL, Aliases: []string{"canary-v2"}, NeedsWAV: false, TargetPath: "/inference"},
		},
		health:       newHealthState(),
		defaultModel: "canary-v2",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("canary-v2", true)

	body, ct := buildMultipart(t, map[string]string{}, "audio.wav", "RIFF"+string(make([]byte, 36)))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"hello"`)) {
		t.Errorf("expected transcript 'hello' in response, got: %s", rr.Body.String())
	}
	// Canary's "timestamps":null must be stripped — gateway contract is {"text":"..."}
	if bytes.Contains(rr.Body.Bytes(), []byte("timestamps")) {
		t.Errorf("timestamps field leaked into response: %s", rr.Body.String())
	}
}

// --- Commit D: proxy-cancel errors point ---

// TestTranscriptionHandler_UpstreamCanceled_EmitsError verifies the proxy
// ErrorHandler fires an errors point with kind=stt_upstream_canceled when the
// client cancels mid-flight (visibility hook from Commit D — without it these
// failures disappear from Influx, confirmed 30d sweep) but that a pure client
// cancel is NOT treated as a backend fault: the backend stays healthy, no
// stt_backend_5xx is emitted, OnRequestFailed is not called, and no fallback
// retry fires. A context.Canceled is client-initiated by definition (transport
// timeouts surface as context.DeadlineExceeded), so it must never demote a
// healthy backend or burn a wasteful fallback GPU inference for a client that
// is already gone.
// Not parallel-safe: mutates core.OnError / OnRequestFailed.
func TestTranscriptionHandler_UpstreamCanceled_EmitsError(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	failures, restoreFail := withCapturedOnRequestFailed(t)
	defer restoreFail()

	// Backend blocks until the test releases it — the client cancels before
	// the backend ever responds. We release after asserting so Close() doesn't
	// deadlock on the still-active handler goroutine.
	blockStart := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(blockStart) })
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	defer backend.Close()
	defer close(release)

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	// Mark the backend explicitly healthy so a later demotion is detectable
	// (health.get defaults to false for unset names).
	g.health.set("small", true)

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.TranscriptionHandler()(rr, req)
	}()

	// Wait for the proxy to reach the backend, then cancel.
	<-blockStart
	cancel()
	<-done

	if len(*events) == 0 {
		t.Fatal("no OnError event captured on proxy cancel")
	}
	got := (*events)[0]
	if got.Kind != "stt_upstream_canceled" {
		t.Errorf("kind: want stt_upstream_canceled, got %q", got.Kind)
	}
	if got.Source != "stt" {
		t.Errorf("source: want stt, got %q", got.Source)
	}

	// Regression (error-stt-upstream-canceled): a client cancel must NOT be
	// misrouted through the 5xx backend-failure path. Before the fix the
	// ErrorHandler wrote 502, so the caller demoted the backend, emitted a
	// stt_backend_5xx event, and (having no alternative) called
	// OnRequestFailed(errTypeSTTError) — inflating the Din error-rate alerter
	// on a benign double-tap. None of that may happen now.
	for _, e := range *events {
		if e.Kind == "stt_backend_5xx" {
			t.Errorf("client cancel wrongly emitted stt_backend_5xx: %+v", e)
		}
	}
	if !g.health.get("small") {
		t.Error("client cancel wrongly demoted the backend (health=false); a cancel is not a backend fault")
	}
	if len(*failures) != 0 {
		t.Errorf("OnRequestFailed must NOT be called on a pure client cancel, got %d call(s): %v", len(*failures), *failures)
	}
	if rr.Code != statusClientClosed {
		t.Errorf("status: want %d (client closed), got %d", statusClientClosed, rr.Code)
	}
}

// TestTranscriptionHandler_UpstreamUnreachable_EmitsBackendError verifies
// non-cancel transport errors are classified as stt_backend_error (not
// stt_upstream_canceled), keeping the closed vocabulary coherent.
func TestTranscriptionHandler_UpstreamUnreachable_EmitsBackendError(t *testing.T) {
	events, restoreErr := withCapturedOnError(t)
	defer restoreErr()
	failures, restoreFail := withCapturedOnRequestFailed(t)
	defer restoreFail()

	g := &Gateway{
		backends: []Backend{
			// Port 1: guaranteed closed.
			{Name: "small", URL: "http://127.0.0.1:1", Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if len(*events) == 0 {
		t.Fatal("no OnError event captured for unreachable backend")
	}
	if (*events)[0].Kind != "stt_backend_error" {
		t.Errorf("kind: want stt_backend_error, got %q", (*events)[0].Kind)
	}
	if len(*failures) == 0 {
		t.Error("OnRequestFailed not called on transport error")
	}
}

// --- rewriteMultipart LanguageOverride and InjectVerboseJSON ---

// fieldValues returns a map of field name → value for all non-file parts.
func fieldValues(t *testing.T, body []byte, ct string) map[string]string {
	t.Helper()
	_, params, _ := parseMediaType(ct)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	vals := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("fieldValues: read part: %v", err)
		}
		if part.FileName() == "" {
			data, _ := io.ReadAll(part)
			vals[part.FormName()] = string(data)
		}
		part.Close()
	}
	return vals
}

func TestRewriteMultipart_LanguageOverride(t *testing.T) {
	// LanguageOverride strips the client language and injects the given code.
	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		LanguageOverride: "cs",
	})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	vals := fieldValues(t, rewritten, newCT)
	if vals["language"] != "cs" {
		t.Errorf("language: want cs, got %q", vals["language"])
	}
}

func TestRewriteMultipart_InjectVerboseJSON(t *testing.T) {
	// InjectVerboseJSON adds response_format=verbose_json after stripping any client value.
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		InjectVerboseJSON: true,
	})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	vals := fieldValues(t, rewritten, newCT)
	if vals["response_format"] != "verbose_json" {
		t.Errorf("response_format: want verbose_json, got %q", vals["response_format"])
	}
}
