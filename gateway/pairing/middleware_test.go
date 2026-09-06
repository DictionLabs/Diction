package pairing

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
	RegisterRoutes(mux, ks)
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

func TestModeFromEnv(t *testing.T) {
	cases := map[string]Mode{
		"":         ModeOptional,
		"optional": ModeOptional,
		"garbage":  ModeOptional,
		"off":      ModeOff,
		"OFF":      ModeOff,
		"required": ModeRequired,
		"Require":  ModeRequired,
	}
	for val, want := range cases {
		t.Setenv("DICTION_GATEWAY_AUTH", val)
		if got := ModeFromEnv(); got != want {
			t.Errorf("DICTION_GATEWAY_AUTH=%q → %q, want %q", val, got, want)
		}
	}
}

func TestKeyMiddlewareMatrix(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	graceKey, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	forceRotate(ks)
	if _, _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	current, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}

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
			h := KeyMiddleware(ks, tc.required, func(w http.ResponseWriter, r *http.Request) {
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

func TestKeyMiddlewareNilStorePasses(t *testing.T) {
	passed := false
	h := KeyMiddleware(nil, false, func(w http.ResponseWriter, r *http.Request) { passed = true })
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !passed {
		t.Fatal("nil keystore in optional mode must pass")
	}
}

func TestAuthKeyEndpoint(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	graceToken, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	forceRotate(ks)
	if _, _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	srv := pairingTestServer(t, ks)

	resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/key", graceToken)
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
	if !ks.Verify(body.Key) {
		t.Fatalf("exchange returned a token that does not verify: %q", body.Key)
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
	if resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/key", body.Key); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
}

// TestAuthKeyEndpoint_RejectsExpiredToken is Expiry policy rule 1 at the
// HTTP level: an expired token gets 401 on /v1/auth/key too — there are no
// refresh-token semantics.
func TestAuthKeyEndpoint_RejectsExpiredToken(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, time.Millisecond)
	token, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	srv := pairingTestServer(t, ks)
	resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/key", token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token on /v1/auth/key = %d, want 401", resp.StatusCode)
	}
}

// TestRotateEndpoint_RejectsExpiredToken mirrors the above for /v1/auth/rotate.
func TestRotateEndpoint_RejectsExpiredToken(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, time.Millisecond)
	token, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	srv := pairingTestServer(t, ks)
	resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token on /v1/auth/rotate = %d, want 401", resp.StatusCode)
	}
}

func TestRotateEndpoint(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	oldToken, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	forceRotate(ks)
	srv := pairingTestServer(t, ks)

	resp := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", oldToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d", resp.StatusCode)
	}
	var body struct {
		Key                string `json:"key"`
		PreviousValidUntil string `json:"previous_valid_until"`
		Coalesced          bool   `json:"coalesced"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Key == oldToken || body.Key == "" || !ks.Verify(body.Key) {
		t.Fatalf("rotate returned %q", body.Key)
	}
	if body.Coalesced {
		t.Fatal("a real rotation must not carry coalesced:true")
	}
	if _, err := time.Parse(time.RFC3339, body.PreviousValidUntil); err != nil {
		t.Fatalf("previous_valid_until %q not RFC3339: %v", body.PreviousValidUntil, err)
	}
	// The old token can still rotate again — but immediately after a real
	// rotation, the current secret is brand new, so this second call
	// coalesces rather than rotating again.
	resp2 := doAuthed(t, http.MethodPost, srv.URL+"/v1/auth/rotate", oldToken)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("grace-key rotate status = %d, want 200", resp2.StatusCode)
	}
	var body2 struct {
		Key                string `json:"key"`
		PreviousValidUntil string `json:"previous_valid_until"`
		Coalesced          bool   `json:"coalesced"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if !body2.Coalesced {
		t.Fatal("second rotate within 24h must coalesce")
	}
	if body2.PreviousValidUntil != "" {
		t.Fatal("coalesced rotate must omit previous_valid_until")
	}
	if resp := doAuthed(t, http.MethodGet, srv.URL+"/v1/auth/rotate", body.Key); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET rotate status = %d, want 405", resp.StatusCode)
	}
}

func TestRotateEndpointPinned(t *testing.T) {
	ks, _ := newTestKeyStore(t, "dk_pinned", time.Hour, 0)
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
