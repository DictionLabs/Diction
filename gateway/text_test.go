package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// --- handleTextProcess ---

func TestTextProcess_LLMNotConfigured(t *testing.T) {
	llm := llmConfig{Enabled: false}
	handler := handleTextProcess(llm)

	req := httptest.NewRequest(http.MethodPost, "/v1/text/process", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["error"] != "llm_not_configured" {
		t.Errorf("error: want llm_not_configured, got %s", resp["error"])
	}
}

func TestTextProcess_E2EHeaderRejected(t *testing.T) {
	srv := makeLLMServer(t, "cleaned text")
	llm := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test", Prompt: "Fix."}

	handler := handleTextProcess(llm)
	req := httptest.NewRequest(http.MethodPost, "/v1/text/process", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Diction-E2E", "1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rr.Code)
	}
}

func TestTextProcess_EmptyTextRejected(t *testing.T) {
	srv := makeLLMServer(t, "ok")
	llm := llmConfig{Enabled: true, BaseURL: srv.URL, Model: "test", Prompt: "Fix."}

	handler := handleTextProcess(llm)
	req := httptest.NewRequest(http.MethodPost, "/v1/text/process", strings.NewReader(`{"text":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rr.Code)
	}
}

func TestTextProcess_PlaintextAccepted(t *testing.T) {
	srv := makeLLMServer(t, "Cleaned transcript.")
	llm := llmConfig{
		Enabled:  true,
		BaseURL:  srv.URL,
		Model:    "test",
		Prompt:   DefaultPromptCleanup,
		PromptEdit:         DefaultPromptEdit,
		PromptEditSelected: DefaultPromptEditSelected,
		PromptSuggest:      DefaultPromptSuggest,
	}

	handler := handleTextProcess(llm)
	body := `{"text":"uh so yeah the meeting went well"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/text/process", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["text"] == "" {
		t.Error("expected non-empty text in response")
	}
	if resp["mode"] != "transcribe" {
		t.Errorf("mode: want transcribe, got %s", resp["mode"])
	}
}

func TestTextProcess_MethodNotAllowed(t *testing.T) {
	llm := llmConfig{Enabled: true, Model: "test"}
	handler := handleTextProcess(llm)

	req := httptest.NewRequest(http.MethodGet, "/v1/text/process", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405, got %d", rr.Code)
	}
}

// --- handleTextSuggest ---

func TestTextSuggest_LLMNotConfigured(t *testing.T) {
	llm := llmConfig{Enabled: false}
	handler := handleTextSuggest(llm)

	req := httptest.NewRequest(http.MethodPost, "/v1/text/suggest", strings.NewReader(`{"selected":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", rr.Code)
	}
}

func TestTextSuggest_SoftFail(t *testing.T) {
	// LLM server returns error -- suggest should still return 200 with empty suggestions.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	llm := llmConfig{
		Enabled:       true,
		BaseURL:       srv.URL,
		Model:         "test",
		PromptSuggest: DefaultPromptSuggest,
	}
	handler := handleTextSuggest(llm)

	body := `{"selected":"hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/text/suggest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["suggestions"]; !ok {
		t.Error("expected suggestions key in response")
	}
}

// --- /v1/models capabilities ---

func TestModelsCapabilities_LLMDisabled(t *testing.T) {
	os.Setenv("TRIAL_DB_PATH", t.TempDir()+"/trials.json")
	defer os.Unsetenv("TRIAL_DB_PATH")
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("TEXT_ROUTES_OPEN")

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	capsRaw, ok := body["capabilities"]
	if !ok {
		t.Fatal("expected capabilities key in /v1/models response")
	}

	var caps map[string]bool
	json.Unmarshal(capsRaw, &caps)
	if caps["llm"] {
		t.Error("capabilities.llm: want false, got true")
	}
	if caps["text_process"] {
		t.Error("capabilities.text_process: want false, got true")
	}
	if caps["text_suggest"] {
		t.Error("capabilities.text_suggest: want false, got true")
	}

	// Existing data[] and providers[] must still be present.
	if _, ok := body["data"]; !ok {
		t.Error("expected data key preserved in /v1/models response")
	}
}

func TestModelsCapabilities_LLMEnabledRoutesOpen(t *testing.T) {
	os.Setenv("TRIAL_DB_PATH", t.TempDir()+"/trials.json")
	os.Setenv("LLM_BASE_URL", "http://localhost:99999/v1")
	os.Setenv("LLM_MODEL", "test-model")
	os.Setenv("TEXT_ROUTES_OPEN", "true")
	defer func() {
		os.Unsetenv("TRIAL_DB_PATH")
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
		os.Unsetenv("TEXT_ROUTES_OPEN")
	}()

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&body)

	var caps map[string]bool
	json.Unmarshal(body["capabilities"], &caps)

	if !caps["llm"] {
		t.Error("capabilities.llm: want true, got false")
	}
	if !caps["text_process"] {
		t.Error("capabilities.text_process: want true, got false")
	}
	if !caps["text_suggest"] {
		t.Error("capabilities.text_suggest: want true, got false")
	}
}

// --- textRoutesMiddleware fail-closed guard ---

func TestTextRoutes_FailClosed_NoAuth_NoOpen(t *testing.T) {
	os.Setenv("TRIAL_DB_PATH", t.TempDir()+"/trials.json")
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("TEXT_ROUTES_OPEN")
	os.Unsetenv("AUTH_ENABLED")
	defer func() {
		os.Unsetenv("TRIAL_DB_PATH")
	}()

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"hello"}`)
	resp, err := http.Post(srv.URL+"/v1/text/process", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/text/process: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: want 403, got %d", resp.StatusCode)
	}
	var respBody map[string]string
	json.NewDecoder(resp.Body).Decode(&respBody)
	if respBody["error"] != "text_routes_closed" {
		t.Errorf("error: want text_routes_closed, got %s", respBody["error"])
	}
}

// --- helpers ---

// makeLLMServer creates a test HTTP server that returns the given response text
// as an OpenAI chat completions response.
func makeLLMServer(t *testing.T, responseText string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": responseText}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}
