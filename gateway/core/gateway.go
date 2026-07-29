package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ImageRef is set at build time via -ldflags -X. It records the canonical image
// reference (e.g. ghcr.io/dictionlabs/gateway:v1.2.3) so /health can expose it.
var ImageRef = "unknown"

func EnvFloatOrDefault(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// defaultStreamIdleTimeout is the fallback inter-frame gap for WebSocket
// audio streams. 45s is generous: healthy streams send frames every ~100ms.
const defaultStreamIdleTimeout = 45 * time.Second

// Config holds all gateway configuration.
type Config struct {
	Backends      []Backend
	DefaultModel  string
	FallbackModel string
	EnglishModel  string
	ParakeetModel string
	CohereModel   string
	MaxBodySize   int64

	// StreamIdleTimeout bounds the gap between successive WebSocket frames on
	// /v1/audio/stream. Zero → falls back to defaultStreamIdleTimeout.
	// Healthy streams send an audio frame every ~100ms, so 45s is generous.
	StreamIdleTimeout time.Duration

	// ProfileStore enables per-device language history for auto-detect routing.
	// Nil in community builds without MariaDB — auto-detect always falls back to whisper_safe.
	ProfileStore *ProfileStore
}

// Gateway holds runtime state: backends, health, config.
type Gateway struct {
	backends      []Backend
	health        *healthState
	defaultModel  string
	fallbackModel string
	englishModel  string
	parakeetModel string
	cohereModel   string
	maxBodySize   int64
	profileStore  *ProfileStore

	// streamIdleTimeout bounds inter-frame gap on /v1/audio/stream. See Config.
	// Tests override the field directly after construction.
	streamIdleTimeout time.Duration

	// OnTranscription is an optional hook called after each successful transcription.
	// model is the backend name, whisperMs is inference latency, chars is transcript length,
	// durationMs is audio duration parsed from the WAV header (0 if unavailable).
	// enhance and e2e indicate whether LLM post-processing and E2E encryption were requested.
	// Leave nil in community builds.
	OnTranscription func(ctx context.Context, model string, whisperMs int64, chars int, durationMs int64, enhance, e2e bool)

	// DeviceHashFromContext returns the SHA-256 hex device hash for the current request.
	// Wired by the private gateway main() to read from the request log entry; nil in community builds.
	DeviceHashFromContext func(ctx context.Context) string

	// OnAutoDetect is called after the auto-detect routing decision (tier) and optionally again
	// when the detected language arrives from verbose_json. Either tier or lang may be empty.
	// Wired by the private gateway main() to write fields into the request log entry.
	OnAutoDetect func(ctx context.Context, tier, lang string)

	// OnStreamingCodec is called at the start of each /v1/audio/stream connection with
	// the effective codec ("pcm" or "opus") and the subprotocol negotiation outcome
	// ("accepted" | "offered" | "unavailable" | "").
	// Wired by the private gateway main() to record codec adoption metrics.
	OnStreamingCodec func(ctx context.Context, codec string, negotiation string)
}

// NewGateway creates a Gateway and starts the background health checker.
// If CUSTOM_BACKEND_URL is set, the custom backend is prepended and becomes the default.
func NewGateway(cfg Config) *Gateway {
	backends := cfg.Backends
	defaultModel := cfg.DefaultModel
	if custom := CustomBackendFromEnv(); custom != nil {
		backends = append([]Backend{*custom}, backends...)
		defaultModel = "custom"
	}
	idle := cfg.StreamIdleTimeout
	if idle <= 0 {
		idle = defaultStreamIdleTimeout
	}
	g := &Gateway{
		backends:          backends,
		health:            newHealthState(),
		defaultModel:      defaultModel,
		fallbackModel:     cfg.FallbackModel,
		englishModel:      cfg.EnglishModel,
		parakeetModel:     cfg.ParakeetModel,
		cohereModel:       cfg.CohereModel,
		maxBodySize:       cfg.MaxBodySize,
		streamIdleTimeout: idle,
		profileStore:      cfg.ProfileStore,
	}
	g.startHealthChecker()
	return g
}

// resolveBackend maps a model name/alias to a backend URL and its config.
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

// HealthHandler returns the handler for GET /health.
func (g *Gateway) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(ImageRef, "omachala") {
			fmt.Fprintf(w, `{"status":"ok","image_ref":%q,"deprecated":"this image is hosted under omachala; please switch to ghcr.io/dictionlabs/gateway"}`, ImageRef)
		} else {
			fmt.Fprintf(w, `{"status":"ok","image_ref":%q}`, ImageRef)
		}
	}
}

// CatchAllHandler returns the root / 404 handler.
func (g *Gateway) CatchAllHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"service":"diction-gateway","docs":"https://diction.one"}`))
	}
}

// --- Environment helpers ---

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
