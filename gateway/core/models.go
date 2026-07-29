package core

import (
	"encoding/json"
	"net/http"
	"strings"
)

type modelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type providerInfo struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Models []modelInfo `json:"models"`
}

// openaiModel is one entry in the OpenAI-compatible /v1/models data[] array.
type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`   // always "model"
	Created int64  `json:"created"`  // always 0 — backends are static config, no install date
	OwnedBy string `json:"owned_by"` // HuggingFace org prefix (e.g. "Systran", "nvidia") or "custom"
}

type modelsResponse struct {
	Object    string         `json:"object"`    // "list" — OpenAI list envelope
	Data      []openaiModel  `json:"data"`      // OpenAI-compatible model list
	Providers []providerInfo `json:"providers"` // Diction legacy grouping (consumed by iOS app)
}

// provider display names
var providerNames = map[string]string{
	"whisper":  "Faster Whisper",
	"parakeet": "NVIDIA Parakeet",
	"canary":   "NVIDIA Canary",
	"cohere":   "Cohere",
}

// ModelsHandler returns the handler for GET /v1/models.
//
// Response shape is a superset: OpenAI-compatible `object` + `data[]` for SDK clients
// (openai-python, openai-node, LangChain, Speaches-targeting tools), alongside
// Diction's legacy `providers[]` grouping still consumed by the iOS app.
func (g *Gateway) ModelsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// OpenAI-compatible data[]: one entry per configured backend, no health filtering.
		data := make([]openaiModel, 0, len(g.backends))
		for _, b := range g.backends {
			id := b.CanonicalID
			if id == "" {
				id = b.Name
			}
			owner := "custom"
			if idx := strings.Index(id, "/"); idx > 0 {
				owner = id[:idx]
			}
			data = append(data, openaiModel{
				ID:      id,
				Object:  "model",
				Created: 0,
				OwnedBy: owner,
			})
		}

		// Legacy providers[]: grouped by provider kind, reflects runtime health.
		grouped := make(map[string][]modelInfo)
		providerOrder := make([]string, 0)
		for _, b := range g.backends {
			m := modelInfo{
				ID:          b.Name,
				Name:        b.DisplayName,
				Description: b.Description,
				Available:   g.health.get(b.Name),
			}
			p := b.Provider
			if p == "" {
				p = "whisper"
			}
			if _, exists := grouped[p]; !exists {
				providerOrder = append(providerOrder, p)
			}
			grouped[p] = append(grouped[p], m)
		}

		providers := make([]providerInfo, 0, len(grouped))
		for _, p := range providerOrder {
			name := providerNames[p]
			if name == "" {
				name = p
			}
			providers = append(providers, providerInfo{
				ID:     p,
				Name:   name,
				Models: grouped[p],
			})
		}

		resp := modelsResponse{
			Object:    "list",
			Data:      data,
			Providers: providers,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
