package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
	"github.com/DictionLabs/Diction/gateway/core/chat"
	"github.com/DictionLabs/Diction/gateway/pairing"
)

// textRoutesMiddleware is the fail-closed /v1/text/* guard (R12), a speed bump, not security.
func textRoutesMiddleware(routesOpen bool) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		if !routesOpen {
			return func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(textRoutesClosedBody))
			}
		}
		return next
	}
}

const textRoutesClosedBody = `{"error":"text_routes_closed","hint":"Set TEXT_ROUTES_OPEN=true, or DICTION_GATEWAY_AUTH=required and pair the app, to enable /v1/text/* routes. See AGENTS.md."}`

// openRoute is the fallthrough for routes that need no auth beyond the pairing key.
func openRoute(next http.HandlerFunc) http.HandlerFunc { return next }

// withGatewayKey lets a valid pairing key bypass another guard; required mode rejects keyless.
func withGatewayKey(
	ks *pairing.KeyStore, mode pairing.Mode,
	fallthroughMW func(http.HandlerFunc) http.HandlerFunc,
) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		fallback := fallthroughMW(next)
		if ks == nil {
			return fallback
		}
		return func(w http.ResponseWriter, r *http.Request) {
			if ks.VerifyRequest(r) {
				next(w, r)
				return
			}
			if mode == pairing.ModeRequired {
				pairing.WriteRequired(w)
				return
			}
			fallback(w, r)
		}
	}
}

// withEnhanceTimeout wraps a postProcess function with a hard deadline so raw fallback fires.
func withEnhanceTimeout(
	fn func(ctx context.Context, text, contextJSON, intent string) (string, string, error),
	timeout time.Duration) func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
	return func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return fn(deadlineCtx, text, contextJSON, intent)
	}
}

// Community enhance budgets differ from cloud's (2500/8000) on purpose (slower self-hosted LLMs).
const (
	defaultEnhanceTimeoutMs     = 20000
	defaultLiveEnhanceTimeoutMs = 8000
)

// enhanceBudget applies one budget to the closure; a value <= 0 means no limit (0 must not expire).
func enhanceBudget(
	fn func(ctx context.Context, text, contextJSON, intent string) (string, string, error),
	ms int, label string) func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
	if ms <= 0 {
		log.Printf("LLM %s enhance budget disabled (<=0) — a slow LLM can stall for as long as its own client allows", label)
		return fn
	}
	return withEnhanceTimeout(fn, time.Duration(ms)*time.Millisecond)
}

// loadKeyStore builds the pairing keystore; only required mode treats a failure as fatal.
func loadKeyStore(mode pairing.Mode) (*pairing.KeyStore, error) {
	keyStore, err := pairing.KeyStoreFromEnv(mode)
	if err != nil {
		// Optional mode must survive an unwritable key path: an upgrade never breaks a deploy.
		if mode == pairing.ModeRequired {
			return nil, fmt.Errorf("gateway pairing (DICTION_GATEWAY_AUTH=required): %w", err)
		}
		log.Printf("warning: gateway pairing disabled: %v (set DICTION_KEY_PATH to a writable path)", err)
		return nil, nil
	}
	return keyStore, nil
}

type postProcessFn = func(context.Context, string, string, string) (string, string, error)

// enhanceClosures returns the inline and post-delivery closures, both nil when the LLM is off.
func enhanceClosures(llm llmConfig, inlineMs, liveMs int) (postProcess, postProcessLive postProcessFn) {
	// Stay nil when off: core/streaming.go gates the split answer on `postProcess != nil`.
	if !llm.Enabled {
		return nil, nil
	}
	inner := llm.postProcessor()
	return enhanceBudget(inner, inlineMs, "inline"), enhanceBudget(inner, liveMs, "post-delivery")
}

// capsFor builds the advertised capabilities and the per-request upgrade for valid-key callers.
func capsFor(
	llm llmConfig, chatEngine *chat.ChatEngine, keyStore *pairing.KeyStore,
	gw *core.Gateway, textRoutesOpen bool, pairingMode pairing.Mode, liveEnhanceTimeoutMs int,
) func(*http.Request) capabilityFlags {
	caps := capabilityFlags{
		llmEnabled: llm.Enabled,
		// Advertise text routes only if every caller reaches them: optional-mode keyless gets 403.
		textRoutes:     llm.Enabled && (textRoutesOpen || pairingMode == pairing.ModeRequired),
		pairing:        keyStore != nil,
		keyRotation:    keyStore != nil && !keyStore.Pinned(),
		postDeliveryMs: liveEnhanceTimeoutMs,
	}
	// Live text: the app asks for preview=cloud only when this says so.
	caps.preview, caps.previewOpus = gw.PreviewCapabilities()
	// Chat sits behind the same guard as the text routes.
	chatReachable := textRoutesOpen || pairingMode == pairing.ModeRequired
	caps.chat = chatEngine.Enabled() && chatReachable
	// A caller with a valid pairing key gets the text routes, so tell it so.
	return func(r *http.Request) capabilityFlags {
		if !chatReachable && (llm.Enabled || chatEngine.Enabled()) && keyStore.VerifyRequest(r) {
			upgraded := caps
			upgraded.textRoutes = llm.Enabled
			upgraded.chat = chatEngine.Enabled()
			return upgraded
		}
		return caps
	}
}

// logPairing logs the pairing key state and prints the QR.
func logPairing(keyStore *pairing.KeyStore) {
	log.Printf("gateway pairing key active (fingerprint %s, rotation=%v)", keyStore.Fingerprint(), !keyStore.Pinned())
	publicURL := core.EnvOrDefault("PUBLIC_URL", "")
	if u, err := url.Parse(publicURL); err == nil && u != nil && (u.Path != "" || u.RawQuery != "") {
		log.Printf("warning: PUBLIC_URL should be scheme://host[:port] only; the app drops paths and query strings")
	}
	if token, err := keyStore.IssueToken(); err != nil {
		log.Printf("warning: could not mint pairing token: %v", err)
	} else {
		pairing.PrintQR(publicURL, token)
	}
}

// buildMux reads env config, wires all handlers, returns the mux and port.
func buildMux() (http.Handler, string, error) {
	port := core.EnvOrDefault("GATEWAY_PORT", "8080")
	defaultModel := core.EnvOrDefault("DEFAULT_MODEL", "small")
	maxBodySize := int64(core.EnvIntOrDefault("MAX_BODY_SIZE", 209715200))

	pairingMode := pairing.ModeFromEnv()
	pairing.WarnDeprecatedEnv()
	keyStore, err := loadKeyStore(pairingMode)
	if err != nil {
		return nil, "", err
	}
	pairingState := core.PairingReady
	if keyStore == nil && pairingMode != pairing.ModeOff {
		pairingState = core.PairingDisabled
	}
	core.LogOpenAuthWarning(string(pairingMode), pairingState)

	gw := core.NewGateway(core.Config{
		Backends:     core.DefaultBackends(),
		DefaultModel: defaultModel,
		MaxBodySize:  maxBodySize,
	})

	llm := llmConfigFromEnv()
	textRoutesOpen := core.EnvBoolOrDefault("TEXT_ROUTES_OPEN", false)
	enhanceTimeoutMs := core.EnvIntOrDefault("DICTION_ENHANCE_TIMEOUT_MS", defaultEnhanceTimeoutMs)
	liveEnhanceTimeoutMs := core.EnvIntOrDefault("DICTION_LIVE_ENHANCE_TIMEOUT_MS", defaultLiveEnhanceTimeoutMs)
	postProcess, postProcessLive := enhanceClosures(llm, enhanceTimeoutMs, liveEnhanceTimeoutMs)

	// Chat about a shared text: its own backend (CHAT_*), else the LLM above.
	chatEngine := chat.ChatEngineFromEnv(core.EnvOrDefault)
	capsForRequest := capsFor(llm, chatEngine, keyStore, gw, textRoutesOpen, pairingMode, liveEnhanceTimeoutMs)

	audioMW := withGatewayKey(keyStore, pairingMode, openRoute)
	textMW := withGatewayKey(keyStore, pairingMode,
		textRoutesMiddleware(textRoutesOpen))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", gw.HealthHandler())
	mux.HandleFunc("/v1/models", withCapabilities(gw.ModelsHandler(), capsForRequest))
	mux.HandleFunc("/v1/audio/transcriptions", audioMW(
		gw.TranscriptionHandlerWithPostProcess(postProcess),
	))
	// Transcriptions keep the INLINE closure (no post-delivery phase); the stream splits.
	mux.HandleFunc("/v1/audio/stream", audioMW(
		gw.StreamingHandlerWithSplitEnhance(postProcess, postProcessLive),
	))
	mux.HandleFunc("/v1/text/process", textMW(handleTextProcess(llm)))
	mux.HandleFunc("/v1/text/suggest", textMW(handleTextSuggest(llm)))
	mux.HandleFunc("/v1/text/summarize", textMW(handleTextSummarize(llm)))
	mux.HandleFunc("/v1/chat", textMW(chat.ChatHandler(chatEngine)))
	if keyStore != nil {
		pairing.RegisterRoutes(mux, keyStore)
	}
	mux.HandleFunc("/", gw.CatchAllHandler())

	// Logged because `docker logs` is a self-hoster's only diagnostic surface.
	log.Printf("Diction Gateway starting on :%s (default_model=%s, llm=%v, text_routes=%v, pairing=%s, enhance_ms=%d, live_enhance_ms=%d)", port, defaultModel, llm.Enabled, textRoutesOpen, pairingMode, enhanceTimeoutMs, liveEnhanceTimeoutMs)
	if keyStore != nil {
		logPairing(keyStore)
	}
	return mux, port, nil
}

// newServer sets only ReadHeaderTimeout and IdleTimeout; Read/WriteTimeout would kill streams.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		runAuthCommand()
		return
	}
	mux, port, err := buildMux()
	if err != nil {
		log.Fatalf("%v", err)
	}
	if err := newServer(":"+port, mux).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// runAuthCommand reprints the pairing QR on demand (`docker exec <container> gateway auth`).
func runAuthCommand() {
	mode := pairing.ModeFromEnv()
	if mode == pairing.ModeOff {
		fmt.Println("Pairing is disabled (DICTION_GATEWAY_AUTH=off) — no key to show.")
		os.Exit(1)
	}
	keyStore, err := pairing.KeyStoreFromEnv(mode)
	if err != nil {
		fmt.Printf("gateway pairing unavailable: %v\n", err)
		os.Exit(1)
	}
	token, err := keyStore.IssueToken()
	if err != nil {
		fmt.Printf("could not mint pairing token: %v\n", err)
		os.Exit(1)
	}
	pairing.PrintQR(core.EnvOrDefault("PUBLIC_URL", ""), token)
}
