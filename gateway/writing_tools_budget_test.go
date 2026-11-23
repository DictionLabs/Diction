package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// /v1/models tells the phone how long Writing Tools may take after the raw transcript.
func modelsBody(t *testing.T, env map[string]string) map[string]json.RawMessage {
	t.Helper()
	t.Setenv("DICTION_KEY_PATH", filepath.Join(t.TempDir(), "keys.json"))
	t.Setenv("DICTION_GATEWAY_AUTH", "off")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("DICTION_LIVE_ENHANCE_TIMEOUT_MS", "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func postDeliveryMs(t *testing.T, body map[string]json.RawMessage) (int, bool) {
	t.Helper()
	raw, ok := body["writing_tools"]
	if !ok {
		return 0, false
	}
	var wt struct {
		PostDeliveryMs int `json:"post_delivery_ms"`
	}
	if err := json.Unmarshal(raw, &wt); err != nil {
		t.Fatalf("writing_tools: %v (%s)", err, raw)
	}
	return wt.PostDeliveryMs, true
}

func TestModelsAdvertisesPostDeliveryBudget(t *testing.T) {
	llm := map[string]string{"LLM_BASE_URL": "http://localhost:9/v1", "LLM_MODEL": "m"}

	t.Run("operator value", func(t *testing.T) {
		env := map[string]string{"DICTION_LIVE_ENHANCE_TIMEOUT_MS": "30000"}
		for k, v := range llm {
			env[k] = v
		}
		if ms, ok := postDeliveryMs(t, modelsBody(t, env)); !ok || ms != 30000 {
			t.Errorf("post_delivery_ms = %d (present %v), want 30000", ms, ok)
		}
	})
	t.Run("default", func(t *testing.T) {
		if ms, ok := postDeliveryMs(t, modelsBody(t, llm)); !ok || ms != defaultLiveEnhanceTimeoutMs {
			t.Errorf("post_delivery_ms = %d (present %v), want %d", ms, ok, defaultLiveEnhanceTimeoutMs)
		}
	})
	t.Run("no LLM, no Writing Tools, no field", func(t *testing.T) {
		if _, ok := postDeliveryMs(t, modelsBody(t, nil)); ok {
			t.Error("writing_tools must be absent without an LLM")
		}
	})
}

// /v1/text/process allows 30 s + 1 s per 400 bytes (cap 120 s); /v1/models advertises it.
func TestModelsAdvertisesTextProcessBudget(t *testing.T) {
	llm := map[string]string{"LLM_BASE_URL": "http://localhost:9/v1", "LLM_MODEL": "m"}
	raw, ok := modelsBody(t, llm)["writing_tools"]
	if !ok {
		t.Fatal("writing_tools absent with an LLM")
	}
	var wt map[string]int
	if err := json.Unmarshal(raw, &wt); err != nil {
		t.Fatalf("writing_tools: %v (%s)", err, raw)
	}
	want := map[string]int{
		"post_delivery_ms":          defaultLiveEnhanceTimeoutMs,
		"process_base_ms":           30000,
		"process_per_1000_bytes_ms": 2500,
		"process_max_ms":            120000,
	}
	for k, v := range want {
		if wt[k] != v {
			t.Errorf("%s = %d, want %d (%s)", k, wt[k], v, raw)
		}
	}
	if _, ok := modelsBody(t, nil)["writing_tools"]; ok {
		t.Error("writing_tools must be absent without an LLM")
	}
}

func TestTextProcessBudget(t *testing.T) {
	cases := []struct {
		bytes int
		want  time.Duration
	}{
		{0, 30 * time.Second},
		{400, 31 * time.Second},
		{4000, 40 * time.Second},
		{36000, 120 * time.Second},
		{360000, 120 * time.Second},
		{10 << 20, 120 * time.Second},
	}
	for _, c := range cases {
		if got := textProcessBudget(c.bytes); got != c.want {
			t.Errorf("textProcessBudget(%d) = %v, want %v", c.bytes, got, c.want)
		}
	}
}
