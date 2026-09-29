package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// pairingMux builds the mux with a writable keystore path and the given
// DICTION_GATEWAY_AUTH mode.
func pairingMux(t *testing.T, mode string) http.Handler {
	t.Helper()
	t.Setenv("DICTION_GATEWAY_AUTH", mode)
	t.Setenv("DICTION_KEY_PATH", filepath.Join(t.TempDir(), "gateway-keys.json"))
	t.Setenv("TRIAL_DB_PATH", filepath.Join(t.TempDir(), "trials.json"))
	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	return mux
}

func TestPairing_Optional_KeylessStillPasses(t *testing.T) {
	mux := pairingMux(t, "optional")
	// Keyless request to a guarded audio route must NOT get 401 invalid_key.
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("optional mode must not 401 keyless requests, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPairing_Required_RejectsKeyless(t *testing.T) {
	mux := pairingMux(t, "required")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("required mode must 401 keyless requests, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["reason"] != "invalid_key" {
		t.Fatalf("reason = %q, want invalid_key", body["reason"])
	}
	// /v1/models and /health stay open so an unpaired app can still probe.
	for _, path := range []string{"/v1/models", "/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s must stay open in required mode, got %d", path, rec.Code)
		}
	}
}

func TestPairing_AuthRoutesRegistered(t *testing.T) {
	mux := pairingMux(t, "optional")
	// Without a valid key the route answers 401 (not 404) — proof it is wired.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/key", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/auth/key should be registered and 401 without a key, got %d", rec.Code)
	}
}

func TestPairing_Off_NoAuthRoutes(t *testing.T) {
	mux := pairingMux(t, "off")
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/key", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/v1/auth/key must 404 when pairing is off, got %d", rec.Code)
	}
}

func TestPairing_CapabilitiesAdvertised(t *testing.T) {
	cases := []struct {
		mode                 string
		pairing, keyRotation bool
	}{
		{"optional", true, true},
		{"off", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			mux := pairingMux(t, tc.mode)
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			var resp struct {
				Capabilities struct {
					Pairing     bool `json:"pairing"`
					KeyRotation bool `json:"key_rotation"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode /v1/models: %v", err)
			}
			if resp.Capabilities.Pairing != tc.pairing || resp.Capabilities.KeyRotation != tc.keyRotation {
				t.Fatalf("capabilities = %+v, want pairing=%v key_rotation=%v",
					resp.Capabilities, tc.pairing, tc.keyRotation)
			}
		})
	}
}

func TestPairing_EnvPinned_NoRotationCapability(t *testing.T) {
	t.Setenv("DICTION_GATEWAY_KEY", "dk_pinned_via_env_pinned_via_env_pinned_1")
	mux := pairingMux(t, "optional")
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp struct {
		Capabilities struct {
			Pairing     bool `json:"pairing"`
			KeyRotation bool `json:"key_rotation"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Capabilities.Pairing || resp.Capabilities.KeyRotation {
		t.Fatalf("pinned key: want pairing=true key_rotation=false, got %+v", resp.Capabilities)
	}
	// The pinned key works as a bearer on /v1/auth/key, and rotate refuses 409.
	req = httptest.NewRequest(http.MethodPost, "/v1/auth/rotate", nil)
	req.Header.Set("Authorization", "Bearer dk_pinned_via_env_pinned_via_env_pinned_1")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rotate with pinned key = %d, want 409", rec.Code)
	}
}

func TestPairing_TextRoutesOpenWithPairedKey(t *testing.T) {
	// LLM off here, so the route fails later than the auth layer — the point is
	// that a paired key must NOT hit the text_routes_closed 403 speed bump.
	t.Setenv("DICTION_GATEWAY_KEY", "dk_pinned_via_env_pinned_via_env_pinned_1")
	mux := pairingMux(t, "optional")
	req := httptest.NewRequest(http.MethodPost, "/v1/text/process", nil)
	req.Header.Set("Authorization", "Bearer dk_pinned_via_env_pinned_via_env_pinned_1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("paired key must bypass text_routes_closed, got 403: %s", rec.Body.String())
	}
	// Keyless request keeps today's fail-closed behaviour.
	req = httptest.NewRequest(http.MethodPost, "/v1/text/process", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("keyless /v1/text/process should stay 403, got %d", rec.Code)
	}
}

// An upgrading self-hoster with an LLM configured, TEXT_ROUTES_OPEN unset and
// no key must NOT be told the text routes are available: the app would light
// up Writing Tools and every call would 403. Regression guard — the first cut
// advertised text_process purely on "a keystore exists".
func TestPairing_TextRoutesNotOverAdvertisedToKeylessCallers(t *testing.T) {
	t.Setenv("LLM_MODEL", "some-model")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("LLM_API_KEY", "k")
	mux := pairingMux(t, "optional")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp struct {
		Capabilities struct {
			LLM         bool `json:"llm"`
			TextProcess bool `json:"text_process"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Capabilities.LLM {
		t.Fatalf("llm should be advertised, got %+v", resp.Capabilities)
	}
	if resp.Capabilities.TextProcess {
		t.Fatal("text_process must be false for a keyless caller that would get 403")
	}
	// A keyless call really does 403, which is what the flag now reflects.
	req = httptest.NewRequest(http.MethodPost, "/v1/text/process", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("keyless /v1/text/process = %d, want 403", rec.Code)
	}
}

// The same gateway tells a caller holding a valid pairing key that the text
// routes ARE available, because for that caller they are.
func TestPairing_TextRoutesAdvertisedToKeyedCaller(t *testing.T) {
	const key = "dk_pinned_via_env_pinned_via_env_pinned_1"
	t.Setenv("DICTION_GATEWAY_KEY", key)
	t.Setenv("LLM_MODEL", "some-model")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("LLM_API_KEY", "k")
	mux := pairingMux(t, "optional")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp struct {
		Capabilities struct {
			TextProcess bool `json:"text_process"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Capabilities.TextProcess {
		t.Fatal("a caller with a valid pairing key must see text_process:true")
	}
}

// In required mode nothing keyless can reach the routes at all, so the flat
// advertisement is honest for every caller.
func TestPairing_RequiredModeAdvertisesTextRoutes(t *testing.T) {
	t.Setenv("LLM_MODEL", "some-model")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("LLM_API_KEY", "k")
	mux := pairingMux(t, "required")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp struct {
		Capabilities struct {
			TextProcess bool `json:"text_process"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Capabilities.TextProcess {
		t.Fatal("required mode: every reachable caller is keyed, so text_process must be true")
	}
}
