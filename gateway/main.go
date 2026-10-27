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
	"github.com/DictionLabs/Diction/gateway/pairing"
)

// textRoutesMiddleware returns a middleware that enforces the fail-closed guard
// for /v1/text/* routes (R12) on callers without a valid pairing key (a valid
// key is let through earlier, by withGatewayKey):
//   - TEXT_ROUTES_OPEN=false => 403 {"error":"text_routes_closed"}
//   - TEXT_ROUTES_OPEN=true  => open
//
// The guard is a deliberate speed bump that forces one informed decision rather
// than a security control. The security control is DICTION_GATEWAY_AUTH=required.
func textRoutesMiddleware(routesOpen bool) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		if !routesOpen {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"text_routes_closed","hint":"Set TEXT_ROUTES_OPEN=true, or DICTION_GATEWAY_AUTH=required and pair the app, to enable /v1/text/* routes. See AGENTS.md."}`)) //nolint:errcheck
			}
		}
		return next
	}
}

// openRoute is the fallthrough for routes that need no auth beyond the pairing key.
func openRoute(next http.HandlerFunc) http.HandlerFunc { return next }

// withGatewayKey layers the pairing key (see gateway/pairing) around another
// auth middleware: a valid paired key (current or grace) bypasses it entirely;
// without one the request falls through unchanged, except in required mode
// where keyless requests are rejected with 401 invalid_key. This is what makes
// the paired key a real access control while keeping TEXT_ROUTES_OPEN semantics
// intact for everyone else.
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

// --- Main ---

// buildMux reads configuration from environment variables, wires up all
// handlers, and returns the HTTP mux and the port to listen on.
// Extracted from main() to allow testing without starting a real server.
// withEnhanceTimeout wraps a postProcess-shaped function with a hard deadline.
//
// Every caller already falls back to the raw transcript on a postProcess error, so
// cancelling the context here just makes that fallback trigger on a bounded, visible
// timescale instead of waiting out the LLM client's own timeout — which for a
// self-hosted BYO-LLM endpoint can be minutes.
//
// A timeout <= 0 means "unbounded" and must skip this wrapper entirely: see
// enhanceBudget below for why that case has to be handled by the caller.
func withEnhanceTimeout(
	fn func(ctx context.Context, text, contextJSON, intent string) (string, string, error),
	timeout time.Duration) func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
	return func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
		deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return fn(deadlineCtx, text, contextJSON, intent)
	}
}

// Community enhance budgets. These deliberately DIFFER from the cloud build's
// (2500 / 8000) — do not "fix" them to match without reading this first.
//
//   - Inline (20 s vs cloud's 2.5 s) bounds the pass while the user is still waiting.
//     It governs every path that does NOT use the split answer: /v1/audio/transcriptions,
//     /v1/audio/stream on edit intents (the client gates split_enhance on an empty intent,
//     so edit mode never splits), and older apps that omit the parameter. Cloud's 2.5 s is
//     tuned for Groq at a measured 150-700 ms; a self-hoster's Ollama on CPU routinely
//     takes 5-20 s, so cloud's default here would make those paths fall back to raw almost
//     every time — worse than having no timeout at all, which is where we started.
//
//   - Post-delivery (8 s, same as cloud) bounds the pass after the raw text has already
//     been delivered, so waiting costs the user nothing. It is capped by the CLIENT, not
//     the server: the app's enhanced-frame budget is a hardcoded 9 s that is not
//     backend-aware, so any value above ~9 s is unreachable — the app has already stopped
//     listening. 8 s is the honest number.
//
// Both are env-tunable, and <= 0 means unbounded (see enhanceBudget).
const (
	defaultEnhanceTimeoutMs     = 20000
	defaultLiveEnhanceTimeoutMs = 8000
)

// enhanceBudget applies one budget to the post-process closure, treating a
// non-positive value as "no limit" rather than "no time".
//
// core.EnvIntOrDefault does no range validation, so DICTION_ENHANCE_TIMEOUT_MS=0 would
// otherwise reach context.WithTimeout(ctx, 0) — an already-expired context, failing every
// Writing Tools pass instantly and silently falling back to raw. 0 is the near-universal
// spelling of "no limit", so the self-hoster most likely to type it would get the exact
// opposite of what they asked for, with no error naming the cause.
func enhanceBudget(
	fn func(ctx context.Context, text, contextJSON, intent string) (string, string, error),
	ms int, label string) func(ctx context.Context, text, contextJSON, intent string) (string, string, error) {
	if ms <= 0 {
		log.Printf("LLM %s enhance budget disabled (<=0) — a slow LLM can stall for as long as its own client allows", label)
		return fn
	}
	return withEnhanceTimeout(fn, time.Duration(ms)*time.Millisecond)
}

func buildMux() (http.Handler, string, error) {
	port := core.EnvOrDefault("GATEWAY_PORT", "8080")
	defaultModel := core.EnvOrDefault("DEFAULT_MODEL", "small")
	maxBodySize := int64(core.EnvIntOrDefault("MAX_BODY_SIZE", 209715200))

	// Gateway pairing key (QR pairing + rotation). Default mode is optional:
	// the key is generated, printed, and accepted, but keyless requests still
	// pass — an image upgrade can never lock an existing deploy out.
	pairingMode := pairing.ModeFromEnv()
	pairing.WarnDeprecatedEnv()
	keyStore, err := pairing.KeyStoreFromEnv(pairingMode)
	if err != nil {
		// In the default optional mode a keystore failure (unwritable
		// DICTION_KEY_PATH, read-only rootfs) must not take the gateway down —
		// an image upgrade may never break an existing deploy. Only an explicit
		// required mode fails loudly, because silently running open would
		// contradict the operator's stated intent.
		if pairingMode == pairing.ModeRequired {
			return nil, "", fmt.Errorf("gateway pairing (DICTION_GATEWAY_AUTH=required): %w", err)
		}
		log.Printf("warning: gateway pairing disabled: %v (set DICTION_KEY_PATH to a writable path)", err)
		keyStore = nil
	}

	gw := core.NewGateway(core.Config{
		Backends:     core.DefaultBackends(),
		DefaultModel: defaultModel,
		MaxBodySize:  maxBodySize,
	})

	// LLM post-processing (BYO LLM for self-hosters)
	llm := llmConfigFromEnv()
	textRoutesOpen := core.EnvBoolOrDefault("TEXT_ROUTES_OPEN", false)
	enhanceTimeoutMs := core.EnvIntOrDefault("DICTION_ENHANCE_TIMEOUT_MS", defaultEnhanceTimeoutMs)
	liveEnhanceTimeoutMs := core.EnvIntOrDefault("DICTION_LIVE_ENHANCE_TIMEOUT_MS", defaultLiveEnhanceTimeoutMs)
	// Both stay nil when the LLM is off. A wrapped nil would still be non-nil, and
	// core/streaming.go gates the split answer on `postProcess != nil` — so wrapping
	// unconditionally would turn "no LLM configured" into "an LLM that always fails".
	var postProcess, postProcessLive func(context.Context, string, string, string) (string, string, error)
	if llm.Enabled {
		inner := llm.postProcessor()
		postProcess = enhanceBudget(inner, enhanceTimeoutMs, "inline")
		postProcessLive = enhanceBudget(inner, liveEnhanceTimeoutMs, "post-delivery")
	}

	caps := capabilityFlags{
		llmEnabled: llm.Enabled,
		// Only advertise the text routes when EVERY caller that can reach them
		// gets through. In required mode a request without a valid key never
		// arrives, so pairing alone is enough; in optional mode a keyless
		// caller still hits the text_routes_closed 403, and claiming otherwise
		// would light up Writing Tools in the app for someone whose every call
		// then fails. A caller that does present a valid key is upgraded
		// per-request below.
		textRoutes:     llm.Enabled && (textRoutesOpen || pairingMode == pairing.ModeRequired),
		pairing:        keyStore != nil,
		keyRotation:    keyStore != nil && !keyStore.Pinned(),
		postDeliveryMs: liveEnhanceTimeoutMs,
	}
	// Per-request upgrade: a caller holding a valid pairing key does get the
	// text routes (withGatewayKey lets it past the guard), so tell it so.
	capsForRequest := func(r *http.Request) capabilityFlags {
		if llm.Enabled && !caps.textRoutes && keyStore.VerifyRequest(r) {
			upgraded := caps
			upgraded.textRoutes = true
			return upgraded
		}
		return caps
	}

	audioMW := withGatewayKey(keyStore, pairingMode, openRoute)
	textMW := withGatewayKey(keyStore, pairingMode,
		textRoutesMiddleware(textRoutesOpen))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", gw.HealthHandler())
	mux.HandleFunc("/v1/models", withCapabilities(gw.ModelsHandler(), capsForRequest))
	mux.HandleFunc("/v1/audio/transcriptions", audioMW(
		gw.TranscriptionHandlerWithPostProcess(postProcess),
	))
	// Split answer: the raw final ships the moment STT returns, the enhanced frame
	// follows under the longer post-delivery budget. /v1/audio/transcriptions above
	// deliberately keeps the INLINE closure — it has no post-delivery phase, the user
	// is blocked on the response.
	mux.HandleFunc("/v1/audio/stream", audioMW(
		gw.StreamingHandlerWithSplitEnhance(postProcess, postProcessLive),
	))
	mux.HandleFunc("/v1/text/process", textMW(handleTextProcess(llm)))
	mux.HandleFunc("/v1/text/suggest", textMW(handleTextSuggest(llm)))
	mux.HandleFunc("/v1/text/summarize", textMW(handleTextSummarize(llm)))
	if keyStore != nil {
		pairing.RegisterRoutes(mux, keyStore)
	}
	mux.HandleFunc("/", gw.CatchAllHandler())

	// The enhance budgets are logged because `docker logs` is a self-hoster's ONLY
	// diagnostic surface — the community build wires no error uplink and no metrics.
	// "My cleanup returns raw text" is otherwise indistinguishable from "my LLM is
	// down", and the budget is the first thing to check.
	log.Printf("Diction Gateway starting on :%s (default_model=%s, llm=%v, text_routes=%v, pairing=%s, enhance_ms=%d, live_enhance_ms=%d)", port, defaultModel, llm.Enabled, textRoutesOpen, pairingMode, enhanceTimeoutMs, liveEnhanceTimeoutMs)
	if keyStore != nil {
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
	return mux, port, nil
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
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

// runAuthCommand reprints the pairing QR on demand — `docker exec <container>
// gateway auth` — for an operator who missed it in the one-shot boot log.
// Reads the same env vars and key file the running server already uses, so it
// always reflects the live key without a restart.
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
