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

type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type capabilities struct {
	LLM         bool `json:"llm"`
	TextProcess bool `json:"text_process"`
	TextSuggest bool `json:"text_suggest"`
	Pairing     bool `json:"pairing"`
	KeyRotation bool `json:"key_rotation"`
	Preview     bool `json:"preview"`
	PreviewOpus bool `json:"preview_opus"`
	Chat        bool `json:"chat"`
}

type modelsResponse struct {
	Object       string         `json:"object"`
	Data         []openaiModel  `json:"data"`
	Providers    []providerInfo `json:"providers"`
	Capabilities capabilities   `json:"capabilities"`
}

var providerNames = map[string]string{
	nameWhisper:  "Faster Whisper",
	nameParakeet: "NVIDIA Parakeet",
	nameCanary:   "NVIDIA Canary",
	nameCohere:   "Cohere",
	"qwen":       "Qwen",
}

func (g *Gateway) ModelsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		previewPCM, previewOpus := g.PreviewCapabilities()
		resp := modelsResponse{
			Object:    "list",
			Data:      g.openaiModels(),
			Providers: g.providerGroups(),
			Capabilities: capabilities{
				LLM:         g.llmEnabled,
				TextProcess: g.llmEnabled,
				TextSuggest: g.llmEnabled,
				Pairing:     g.pairingEnabled,
				KeyRotation: g.keyRotationEnabled,
				Preview:     previewPCM,
				PreviewOpus: previewOpus,
				Chat:        g.chatEnabled,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func (g *Gateway) openaiModels() []openaiModel {
	data := make([]openaiModel, 0, len(g.backends))
	for _, b := range g.backends {
		id := b.CanonicalID
		if id == "" {
			id = b.Name
		}
		owner := nameCustom
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
	return data
}

func (g *Gateway) providerGroups() []providerInfo {
	grouped := make(map[string][]modelInfo)
	providerOrder := make([]string, 0)
	for _, b := range g.backends {
		m := modelInfo{
			ID:          b.Name,
			Name:        b.DisplayName,
			Description: b.Description,
			Available:   g.healthy(b.Name),
		}
		p := b.Provider
		if p == "" {
			p = nameWhisper
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
	return providers
}
