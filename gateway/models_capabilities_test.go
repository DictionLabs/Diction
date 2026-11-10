package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeModels stands in for core's ModelsHandler: a JSON body with its own
// capabilities object, which withCapabilities must replace wholesale.
func fakeModels(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(`{"object":"list","data":[],"capabilities":{"llm":true}}`)) //nolint:errcheck
	}
}

func capsFrom(t *testing.T, flags capabilityFlags) map[string]bool {
	t.Helper()
	h := withCapabilities(fakeModels(http.StatusOK), func(*http.Request) capabilityFlags { return flags })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Capabilities
}

func TestWithCapabilities_NineKeysPreviewFalseByDefault(t *testing.T) {
	caps := capsFrom(t, capabilityFlags{})
	want := map[string]bool{
		"llm": false, "text_process": false, "text_suggest": false, "text_summarize": false,
		"formatting": false, "pairing": false, "key_rotation": false,
		"preview": false, "preview_opus": false,
	}
	if len(caps) != len(want) {
		t.Errorf("capabilities = %v, want exactly %v", caps, want)
	}
	for k, v := range want {
		if got, ok := caps[k]; !ok || got != v {
			t.Errorf("capabilities[%q] = %v (present %v), want %v", k, got, ok, v)
		}
	}
}

func TestWithCapabilities_PreviewFlagsAdvertised(t *testing.T) {
	caps := capsFrom(t, capabilityFlags{preview: true, previewOpus: true})
	if !caps["preview"] || !caps["preview_opus"] {
		t.Errorf("preview=%v preview_opus=%v, want both true", caps["preview"], caps["preview_opus"])
	}
}

func TestWithCapabilities_NonOKPassesThroughUntouched(t *testing.T) {
	h := withCapabilities(fakeModels(http.StatusServiceUnavailable), func(*http.Request) capabilityFlags {
		return capabilityFlags{preview: true}
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rr.Code)
	}
	if got, want := rr.Body.String(), `{"object":"list","data":[],"capabilities":{"llm":true}}`; got != want {
		t.Errorf("body = %s, want the base body unchanged %s", got, want)
	}
}

// The community build has no preview source: the real mux says so explicitly.
func TestBuildMux_ModelsAdvertisesNoPreview(t *testing.T) {
	body := modelsBody(t, nil)
	var caps map[string]bool
	if err := json.Unmarshal(body["capabilities"], &caps); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	for _, k := range []string{"preview", "preview_opus"} {
		if got, ok := caps[k]; !ok || got {
			t.Errorf("capabilities[%q] = %v (present %v), want present and false", k, got, ok)
		}
	}
}
