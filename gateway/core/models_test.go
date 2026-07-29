package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsHandler_MethodNotAllowed(t *testing.T) {
	g := testGateway()
	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405, got %d", rr.Code)
	}
}

func TestModelsHandler_ReturnsJSON(t *testing.T) {
	g := testGateway()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %s", ct)
	}

	var resp modelsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Providers) == 0 {
		t.Error("expected at least one provider")
	}
}

func TestModelsHandler_GroupsByProvider(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, Provider: "whisper", DisplayName: "Small"},
			{Name: "medium", Aliases: []string{"medium"}, Provider: "whisper", DisplayName: "Medium"},
			{Name: "parakeet-v3", Aliases: []string{"parakeet-v3"}, Provider: "parakeet", DisplayName: "Parakeet"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Expect 2 providers: whisper (2 models) and parakeet (1 model)
	if len(resp.Providers) != 2 {
		t.Fatalf("providers: want 2, got %d", len(resp.Providers))
	}

	whisperProvider := resp.Providers[0]
	if whisperProvider.ID != "whisper" {
		t.Errorf("first provider ID: want whisper, got %s", whisperProvider.ID)
	}
	if len(whisperProvider.Models) != 2 {
		t.Errorf("whisper models: want 2, got %d", len(whisperProvider.Models))
	}

	parakeetProvider := resp.Providers[1]
	if parakeetProvider.ID != "parakeet" {
		t.Errorf("second provider ID: want parakeet, got %s", parakeetProvider.ID)
	}
	if len(parakeetProvider.Models) != 1 {
		t.Errorf("parakeet models: want 1, got %d", len(parakeetProvider.Models))
	}
}

func TestModelsHandler_ProviderDisplayName(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, Provider: "whisper"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp.Providers[0].Name != "Faster Whisper" {
		t.Errorf("provider name: want 'Faster Whisper', got %s", resp.Providers[0].Name)
	}
}

func TestModelsHandler_AvailabilityReflectsHealth(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, Provider: "whisper"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	// Mark backend as available
	g.health.set("small", true)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if !resp.Providers[0].Models[0].Available {
		t.Error("expected model to be available")
	}

	// Mark backend as unavailable
	g.health.set("small", false)

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr = httptest.NewRecorder()
	g.ModelsHandler()(rr, req)
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp.Providers[0].Models[0].Available {
		t.Error("expected model to be unavailable")
	}
}

// --- OpenAI-compatible /v1/models shape ---

func TestModelsHandler_OpenAIFormat(t *testing.T) {
	// /v1/models must include the OpenAI list envelope (object="list", data[]) so
	// the openai-python / openai-node / LangChain SDKs parse it.
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, CanonicalID: "Systran/faster-whisper-small", Provider: "whisper"},
			{Name: "parakeet-v3", Aliases: []string{"parakeet-v3"}, CanonicalID: "nvidia/parakeet-tdt-0.6b-v3", Provider: "parakeet"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Object != "list" {
		t.Errorf("object: want list, got %q", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Fatal("data[]: want entries, got 0")
	}
	for _, m := range resp.Data {
		if m.ID == "" {
			t.Error("data entry has empty id")
		}
		if m.Object != "model" {
			t.Errorf("data entry object: want model, got %q", m.Object)
		}
		if m.OwnedBy == "" {
			t.Error("data entry has empty owned_by")
		}
	}
}

func TestModelsHandler_OneDataEntryPerBackend(t *testing.T) {
	// Clients see one model entry per configured backend — not one per alias.
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small", "Systran/faster-whisper-small"}, CanonicalID: "Systran/faster-whisper-small"},
			{Name: "parakeet-v3", Aliases: []string{"parakeet-v3", "parakeet", "nvidia/parakeet-tdt-0.6b-v3"}, CanonicalID: "nvidia/parakeet-tdt-0.6b-v3"},
			{Name: "canary-v2", Aliases: []string{"canary-v2", "canary", "nvidia/canary-1b-v2"}, CanonicalID: "nvidia/canary-1b-v2"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if len(resp.Data) != len(g.backends) {
		t.Fatalf("data entries: want %d (one per backend), got %d", len(g.backends), len(resp.Data))
	}
	wantIDs := map[string]bool{
		"Systran/faster-whisper-small": false,
		"nvidia/parakeet-tdt-0.6b-v3":  false,
		"nvidia/canary-1b-v2":          false,
	}
	for _, m := range resp.Data {
		if _, ok := wantIDs[m.ID]; !ok {
			t.Errorf("unexpected id: %q", m.ID)
			continue
		}
		wantIDs[m.ID] = true
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Errorf("missing canonical id: %q", id)
		}
	}
}

func TestModelsHandler_OwnedByFromHFPrefix(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, CanonicalID: "Systran/faster-whisper-small"},
			{Name: "parakeet-v3", Aliases: []string{"parakeet-v3"}, CanonicalID: "nvidia/parakeet-tdt-0.6b-v3"},
			{Name: "custom", Aliases: []string{"custom"}, CanonicalID: ""},           // no canonical, no slash
			{Name: "house-model", Aliases: []string{"house-model"}, CanonicalID: ""}, // fallback to Name
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	byID := map[string]openaiModel{}
	for _, m := range resp.Data {
		byID[m.ID] = m
	}

	if got := byID["Systran/faster-whisper-small"].OwnedBy; got != "Systran" {
		t.Errorf("Systran prefix → owned_by: want Systran, got %q", got)
	}
	if got := byID["nvidia/parakeet-tdt-0.6b-v3"].OwnedBy; got != "nvidia" {
		t.Errorf("nvidia prefix → owned_by: want nvidia, got %q", got)
	}
	// CanonicalID="" → falls back to backend.Name; no slash → owned_by="custom"
	if got := byID["custom"].OwnedBy; got != "custom" {
		t.Errorf("no-slash id → owned_by: want custom, got %q", got)
	}
	if got := byID["house-model"].OwnedBy; got != "custom" {
		t.Errorf("no-slash id fallback → owned_by: want custom, got %q", got)
	}
}

func TestModelsHandler_LegacyProvidersPreserved(t *testing.T) {
	// Adding OpenAI fields must not break the legacy providers[] shape the iOS app parses.
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, CanonicalID: "Systran/faster-whisper-small", Provider: "whisper", DisplayName: "Small", Description: "fast"},
			{Name: "parakeet-v3", Aliases: []string{"parakeet-v3"}, CanonicalID: "nvidia/parakeet-tdt-0.6b-v3", Provider: "parakeet", DisplayName: "Parakeet", Description: "eu"},
		},
		health:       newHealthState(),
		defaultModel: "small",
	}
	g.health.set("small", true)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if len(resp.Providers) != 2 {
		t.Fatalf("providers: want 2, got %d", len(resp.Providers))
	}
	if resp.Providers[0].ID != "whisper" || resp.Providers[0].Name != "Faster Whisper" {
		t.Errorf("provider[0]: want whisper/Faster Whisper, got %s/%s", resp.Providers[0].ID, resp.Providers[0].Name)
	}
	if len(resp.Providers[0].Models) != 1 || resp.Providers[0].Models[0].ID != "small" {
		t.Errorf("provider[0] model: want small, got %+v", resp.Providers[0].Models)
	}
	if !resp.Providers[0].Models[0].Available {
		t.Error("provider[0] model: want Available=true (small is marked healthy)")
	}
	if resp.Providers[1].ID != "parakeet" {
		t.Errorf("provider[1] id: want parakeet, got %s", resp.Providers[1].ID)
	}
}

func TestModelsHandler_EmptyProvider_DefaultsToWhisper(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "small", Aliases: []string{"small"}, Provider: ""}, // empty provider
		},
		health:       newHealthState(),
		defaultModel: "small",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, req)

	var resp modelsResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp.Providers[0].ID != "whisper" {
		t.Errorf("empty provider should default to 'whisper', got %s", resp.Providers[0].ID)
	}
}
