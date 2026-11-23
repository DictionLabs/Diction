package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatStatus(t *testing.T, env map[string]string) int {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{}`)))
	return rec.Code
}

// Without a CHAT_* or LLM_* backend the route exists but answers chat_not_configured.
func TestBuildMux_ChatRouteNotConfigured(t *testing.T) {
	env := map[string]string{
		"TEXT_ROUTES_OPEN": "true", "CHAT_PROVIDER": "", "LLM_BASE_URL": "", "LLM_MODEL": "",
		"DICTION_KEY_PATH": t.TempDir() + "/key", "DICTION_GATEWAY_AUTH": "optional",
	}
	if got := chatStatus(t, env); got != http.StatusServiceUnavailable {
		t.Errorf("/v1/chat with no backend = %d, want 503", got)
	}
}

// With a backend and open text routes the mux advertises chat and routes to the handler.
func TestBuildMux_ChatAdvertisedAndRouted(t *testing.T) {
	env := map[string]string{
		"TEXT_ROUTES_OPEN": "true", "CHAT_PROVIDER": "openai", "CHAT_BASE_URL": "http://127.0.0.1:1/v1",
		"CHAT_MODEL": "m", "DICTION_KEY_PATH": t.TempDir() + "/key", "DICTION_GATEWAY_AUTH": "optional",
	}
	if got := chatStatus(t, env); got != http.StatusBadRequest {
		t.Errorf("/v1/chat with a backend and an empty body = %d, want 400", got)
	}
	if !buildMuxCaps(t, env)["chat"] {
		t.Errorf("capabilities[chat] = false, want true")
	}
}

// Text routes closed and no pairing: chat is not reachable, so it is not advertised.
func TestBuildMux_ChatNotAdvertisedWhenClosed(t *testing.T) {
	env := map[string]string{
		"TEXT_ROUTES_OPEN": "false", "CHAT_PROVIDER": "openai", "CHAT_BASE_URL": "http://127.0.0.1:1/v1",
		"CHAT_MODEL": "m", "DICTION_KEY_PATH": t.TempDir() + "/key", "DICTION_GATEWAY_AUTH": "optional",
	}
	if buildMuxCaps(t, env)["chat"] {
		t.Errorf("capabilities[chat] = true with closed text routes")
	}
	if got := chatStatus(t, env); got != http.StatusForbidden {
		t.Errorf("/v1/chat keyless on closed routes = %d, want 403", got)
	}
}
