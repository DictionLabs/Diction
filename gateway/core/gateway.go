package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var ImageRef = "unknown"

var Version = "dev"

func EnvFloatOrDefault(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

const defaultStreamIdleTimeout = 45 * time.Second

type Config struct {
	Backends      []Backend
	DefaultModel  string
	FallbackModel string
	EnglishModel  string
	ParakeetModel string
	CohereModel   string
	QwenASRModel  string
	MaxBodySize   int64

	StreamIdleTimeout time.Duration

	ProfileStore *ProfileStore

	LLMEnabled bool

	PairingEnabled bool

	KeyRotationEnabled bool

	ChatEnabled bool
}

type Gateway struct {
	backends      []Backend
	health        *healthState
	defaultModel  string
	fallbackModel string
	englishModel  string
	parakeetModel string
	cohereModel   string
	qwenModel     string
	maxBodySize   int64
	profileStore  *ProfileStore
	llmEnabled    bool
	chatEnabled   bool

	pairingEnabled     bool
	keyRotationEnabled bool

	langCapabilityRouting bool

	demotionTTL time.Duration

	streamIdleTimeout time.Duration

	live LiveConfig

	OnTranscription func(ctx context.Context, model string, whisperMs int64, chars int, durationMs int64, enhance, e2e bool)

	OnAudioReceived func(ctx context.Context, durationMs int64)

	DeviceHashFromContext func(ctx context.Context) string

	OnAutoDetect func(ctx context.Context, tier, lang string)

	OnStreamingCodec func(ctx context.Context, codec string, negotiation string)
}

func NewGateway(cfg Config) *Gateway {
	backends := cfg.Backends
	defaultModel := cfg.DefaultModel
	if custom := CustomBackendFromEnv(); custom != nil {
		backends = append([]Backend{*custom}, backends...)
		defaultModel = nameCustom
	}
	idle := cfg.StreamIdleTimeout
	if idle <= 0 {
		idle = defaultStreamIdleTimeout
	}
	g := &Gateway{
		backends:              backends,
		health:                newHealthState(),
		defaultModel:          defaultModel,
		fallbackModel:         cfg.FallbackModel,
		englishModel:          cfg.EnglishModel,
		parakeetModel:         cfg.ParakeetModel,
		cohereModel:           cfg.CohereModel,
		qwenModel:             cfg.QwenASRModel,
		maxBodySize:           cfg.MaxBodySize,
		streamIdleTimeout:     idle,
		profileStore:          cfg.ProfileStore,
		llmEnabled:            cfg.LLMEnabled,
		chatEnabled:           cfg.ChatEnabled,
		pairingEnabled:        cfg.PairingEnabled,
		keyRotationEnabled:    cfg.KeyRotationEnabled,
		langCapabilityRouting: EnvBoolOrDefault("DICTION_LANG_CAPABILITY_ROUTING", true),
		demotionTTL:           sttDemotionTTLFromEnv(),
		live:                  LiveConfigFromEnv(),
	}
	g.startHealthChecker()
	return g
}

func (g *Gateway) resolveBackend(model string) (*url.URL, *Backend) {
	model = strings.TrimSpace(model)
	for i := range g.backends {
		for _, alias := range g.backends[i].Aliases {
			if strings.EqualFold(model, alias) {
				u, err := url.Parse(g.backends[i].URL)
				if err != nil {
					return nil, nil
				}
				return u, &g.backends[i]
			}
		}
	}
	return nil, nil
}

type healthResponse struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	ImageRef   string `json:"image_ref"`
	Deprecated string `json:"deprecated,omitempty"`
}

func (g *Gateway) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := healthResponse{Status: "ok", Version: Version, ImageRef: ImageRef}
		if strings.Contains(ImageRef, "omachala") {
			resp.Deprecated = "this image is hosted under omachala; please switch to ghcr.io/dictionlabs/gateway"
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func (g *Gateway) CatchAllHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"diction-gateway","docs":"https://diction.one"}`))
	}
}

func EnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func EnvIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func EnvBoolOrDefault(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
