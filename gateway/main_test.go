package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// --- buildMux (covers env-var wiring extracted from main) ---

func TestBuildMux_Defaults(t *testing.T) {
	for _, k := range []string{"GATEWAY_PORT", "DEFAULT_MODEL", "MAX_BODY_SIZE", "AUTH_ENABLED"} {
		os.Unsetenv(k)
	}

	mux, port, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
	if port != "8080" {
		t.Errorf("port: want 8080, got %s", port)
	}
}

func TestBuildMux_CustomPort(t *testing.T) {
	os.Setenv("GATEWAY_PORT", "9999")
	defer os.Unsetenv("GATEWAY_PORT")

	_, port, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	if port != "9999" {
		t.Errorf("port: want 9999, got %s", port)
	}
}

func TestBuildMux_RoutesRegistered(t *testing.T) {

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health: want 200, got %d", resp.StatusCode)
	}
}

// --- LLM wiring in buildMux ---

func TestBuildMux_LLM_Disabled(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("LLM_PROMPT")

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
	// No crash when LLM env vars are absent — postProcess is nil.
}

func TestBuildMux_LLM_Enabled(t *testing.T) {
	os.Setenv("LLM_BASE_URL", "http://localhost:99999/v1")
	os.Setenv("LLM_MODEL", "test-model")
	os.Setenv("LLM_PROMPT", "Fix grammar.")
	defer func() {
		os.Unsetenv("LLM_BASE_URL")
		os.Unsetenv("LLM_MODEL")
		os.Unsetenv("LLM_PROMPT")
	}()

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux with LLM enabled")
	}
	// Gateway starts without error when LLM env vars are set.
}

func TestNewServerSetsOnlyHeaderAndIdleTimeouts(t *testing.T) {
	srv := newServer(":9", http.NotFoundHandler())
	if srv.Addr != ":9" || srv.Handler == nil {
		t.Fatalf("addr/handler not set: %q %v", srv.Addr, srv.Handler)
	}
	if srv.ReadHeaderTimeout != 10*time.Second || srv.IdleTimeout != 5*time.Minute {
		t.Fatalf("timeouts: header %v idle %v", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatalf("read/write timeouts must stay unset: %v %v", srv.ReadTimeout, srv.WriteTimeout)
	}
}
