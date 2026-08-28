package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func pairingTestServer(t *testing.T, ks *KeyStore) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterPairingRoutes(mux, ks)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func doAuthed(t *testing.T, method, url, key string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPairingModeFromEnv(t *testing.T) {
	cases := map[string]PairingMode{
		"":         PairingOptional,
		"optional": PairingOptional,
		"garbage":  PairingOptional,
		"off":      PairingOff,
		"OFF":      PairingOff,
		"required": PairingRequired,
		"Require":  PairingRequired,
	}
	for val, want := range cases {
		t.Setenv("DICTION_GATEWAY_AUTH", val)
		if got := PairingModeFromEnv(); got != want {
			t.Errorf("DICTION_GATEWAY_AUTH=%q → %q, want %q", val, got, want)
		}
	}
}

func TestGatewayKeyMiddlewareMatrix(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	valid := ks.CurrentKey()
	graceKey := valid
	if _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	current := ks.CurrentKey()

	cases := []struct {
		name     string
		required bool
		token    string
		wantPass bool
	}{
		{"optional/no-key", false, "", true},
		{"optional/garbage", false, "nonsense", true},
		{"optional/valid", false, current, true},
		{"required/no-key", true, "", false},
		{"required/garbage", true, "nonsense", false},
		{"required/valid", true, current, true},
		{"required/grace", true, graceKey, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passed := false
			h := GatewayKeyMiddleware(ks, tc.required, func(w http.ResponseWriter, r *http.Request) {
				passed = true
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if passed != tc.wantPass {
				t.Fatalf("passed = %v, want %v (status %d)", passed, tc.wantPass, rec.Code)
			}
			if !tc.wantPass {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", rec.Code)
				}
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body["reason"] != "invalid_key" {
					t.Fatalf("reason = %q, want invalid_key", body["reason"])
				}
			}
		})
	}
}

func TestGatewayKeyMiddlewareNilStorePasses(t *testing.T) {
	passed := false
	h := GatewayKeyMiddleware(nil, false, func(w http.ResponseWriter, r *http.Request) { passed = true })
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !passed {
		t.Fatal("nil keystore in optional mode must pass")
	}
}

func TestAuthKeyEndpoint(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	graceKey := ks.CurrentKey()
	if _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	current := ks.CurrentKey()
	srv := pairingTestServer(t, ks)

	// Grace key exchanges for the current key — the multi-device catch-up path.
	resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/key", graceKey)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grace-key exchange status = %d", resp.StatusCode)
	}
	var body struct {
		Key      string `json:"key"`
		Rotation bool   `json:"rotation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Key != current {
		t.Fatalf("exchange returned %q, want current key", body.Key)
	}
	if !body.Rotation {
		t.Fatal("rotation should be true for file-backed keys")
	}

	if resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/key", "dk_bogus"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus key status = %d, want 401", resp.StatusCode)
	}
	if resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/key", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want 401", resp.StatusCode)
	}
	if resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/key", current); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
}

func TestRotateEndpoint(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	old := ks.CurrentKey()
	srv := pairingTestServer(t, ks)

	resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", old)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d", resp.StatusCode)
	}
	var body struct {
		Key                string `json:"key"`
		PreviousValidUntil string `json:"previous_valid_until"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Key == old || body.Key == "" {
		t.Fatalf("rotate returned %q", body.Key)
	}
	if _, err := time.Parse(time.RFC3339, body.PreviousValidUntil); err != nil {
		t.Fatalf("previous_valid_until %q not RFC3339: %v", body.PreviousValidUntil, err)
	}
	// The old key can still rotate (grace keys are first-class).
	if resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", old); resp.StatusCode != http.StatusOK {
		t.Fatalf("grace-key rotate status = %d, want 200", resp.StatusCode)
	}
	if resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/rotate", body.Key); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET rotate status = %d, want 405", resp.StatusCode)
	}
}

func TestRotateEndpointPinned(t *testing.T) {
	ks, _ := newTestKeyStore(t, "dk_pinned", time.Hour)
	srv := pairingTestServer(t, ks)
	resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", "dk_pinned")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("pinned rotate status = %d, want 409", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["reason"] != "env_pinned" {
		t.Fatalf("reason = %q, want env_pinned", body["reason"])
	}
}
