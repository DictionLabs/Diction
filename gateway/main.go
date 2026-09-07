package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
	"github.com/DictionLabs/Diction/gateway/pairing"
)

// --- Trial store (JSON-backed) ---

type trialRecord struct {
	DeviceID  string    `json:"device_id"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
}

type trialStore struct {
	mu      sync.RWMutex
	records map[string]trialRecord
	path    string
}

func newTrialStore(path string) *trialStore {
	s := &trialStore{
		records: make(map[string]trialRecord),
		path:    path,
	}
	if dir := filepath.Dir(path); dir != "" {
		os.MkdirAll(dir, 0755)
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var records []trialRecord
		if json.Unmarshal(data, &records) == nil {
			for _, r := range records {
				s.records[r.DeviceID] = r
			}
		}
	}
	log.Printf("Trial store loaded: %d records from %s", len(s.records), path)
	return s
}

func (s *trialStore) getTrial(deviceID string) (trialRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, exists := s.records[deviceID]
	return r, exists
}

func (s *trialStore) grantTrial(deviceID string, expiresAt time.Time, tokenType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[deviceID] = trialRecord{
		DeviceID:  deviceID,
		GrantedAt: time.Now(),
		ExpiresAt: expiresAt,
		TokenType: tokenType,
	}
	s.save()
}

func (s *trialStore) save() {
	records := make([]trialRecord, 0, len(s.records))
	for _, r := range s.records {
		records = append(records, r)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		log.Printf("trial store: marshal error: %v", err)
		return
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Printf("trial store: write error: %v", err)
		return
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		log.Printf("trial store: rename error: %v", err)
	}
}

// --- Apple JWS verification ---

// Apple Root CA - G3 (EC P-384, valid 2014–2039)
const appleRootCAPEM = `-----BEGIN CERTIFICATE-----
MIICQzCCAcmgAwIBAgIILcX8iNLFS5UwCgYIKoZIzj0EAwMwZzEbMBkGA1UEAwwS
QXBwbGUgUm9vdCBDQSAtIEczMSYwJAYDVQQLDB1BcHBsZSBDZXJ0aWZpY2F0aW9u
IEF1dGhvcml0eTETMBEGA1UECgwKQXBwbGUgSW5jLjELMAkGA1UEBhMCVVMwHhcN
MTQwNDMwMTgxOTA2WhcNMzkwNDMwMTgxOTA2WjBnMRswGQYDVQQDDBJBcHBsZSBS
b290IENBIC0gRzMxJjAkBgNVBAsMHUFwcGxlIENlcnRpZmljYXRpb24gQXV0aG9y
aXR5MRMwEQYDVQQKDApBcHBsZSBJbmMuMQswCQYDVQQGEwJVUzB2MBAGByqGSM49
AgEGBSuBBAAiA2IABJjpLz1AcqTtkyJygRMc3RCV8cWjTnHcFBbZDuWmBSp3ZHtf
TjjTuxxEtX/1H7YyYl3J6YRbTzBPEVoA/VhYDKX1DyxNB0cTddqXl5dvMVztK517
IDvYuVTZXpmkOlEKMaNCMEAwHQYDVR0OBBYEFLuw3qFYM4iapIqZ3r6966/ayySr
MA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQDAgEGMAoGCCqGSM49BAMDA2gA
MGUCMQCD6cHEFl4aXTQY2e3v9GwOAEZLuN+yRhHFD/3meoyhpmvOwgPUnPWTxnS4
at+qIxUCMG1mihDK1A3UT82NQz60imOlM27jbdoXt2QfyFMm+YhidDkLF1vLUagM
6BgD56KyKA==
-----END CERTIFICATE-----`

var appleRootCA *x509.Certificate

func init() {
	block, _ := pem.Decode([]byte(appleRootCAPEM))
	if block == nil {
		log.Fatal("failed to decode Apple Root CA PEM")
	}
	var err error
	appleRootCA, err = x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Fatalf("failed to parse Apple Root CA: %v", err)
	}
}

// Token cache
type cachedToken struct {
	expiresAt time.Time
	claimExp  time.Time
}

var tokenCache sync.Map

const tokenCacheTTL = 5 * time.Minute

type jwsHeader struct {
	Alg string   `json:"alg"`
	X5c []string `json:"x5c"`
}

type jwsPayload struct {
	BundleID       string `json:"bundleId"`
	ExpiresDate    int64  `json:"expiresDate"`
	RevocationDate *int64 `json:"revocationDate"`
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}

type authError struct {
	reason  string
	message string
}

func (e *authError) Error() string { return e.message }

func newAuthError(reason, message string) *authError {
	return &authError{reason: reason, message: message}
}

func verifyAppleJWS(token, bundleID string) error {
	if cached, ok := tokenCache.Load(token); ok {
		entry := cached.(cachedToken)
		if time.Now().Before(entry.expiresAt) && time.Now().Before(entry.claimExp) {
			return nil
		}
		tokenCache.Delete(token)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		return fmt.Errorf("decode header: %w", err)
	}
	var header jwsHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	if header.Alg != "ES256" {
		return fmt.Errorf("unsupported alg: %s", header.Alg)
	}
	if len(header.X5c) == 0 {
		return fmt.Errorf("x5c chain empty")
	}

	certs := make([]*x509.Certificate, len(header.X5c))
	for i, b64 := range header.X5c {
		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fmt.Errorf("decode x5c[%d]: %w", i, err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parse x5c[%d]: %w", i, err)
		}
		certs[i] = cert
	}

	for i := 0; i < len(certs)-1; i++ {
		if err := certs[i].CheckSignatureFrom(certs[i+1]); err != nil {
			return fmt.Errorf("chain verify x5c[%d]→x5c[%d]: %w", i, i+1, err)
		}
	}
	if err := certs[len(certs)-1].CheckSignatureFrom(appleRootCA); err != nil {
		return fmt.Errorf("chain verify x5c[%d]→Apple Root CA: %w", len(certs)-1, err)
	}

	leafKey, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("leaf cert key is not ECDSA")
	}
	if leafKey.Curve != elliptic.P256() {
		return fmt.Errorf("leaf cert key is not P-256")
	}

	sigBytes, err := base64URLDecode(parts[2])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 64 {
		return fmt.Errorf("invalid ES256 signature length: %d", len(sigBytes))
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	signedContent := parts[0] + "." + parts[1]
	hash := sha256.Sum256([]byte(signedContent))
	if !ecdsa.Verify(leafKey, hash[:], r, s) {
		return fmt.Errorf("ES256 signature verification failed")
	}

	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	var payload jwsPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	if payload.BundleID != bundleID {
		return fmt.Errorf("bundleId mismatch: got %q, want %q", payload.BundleID, bundleID)
	}

	expiresTime := time.UnixMilli(payload.ExpiresDate)
	if time.Now().After(expiresTime) {
		return newAuthError("expired_subscription",
			fmt.Sprintf("subscription expired at %s", expiresTime.Format(time.RFC3339)))
	}

	if payload.RevocationDate != nil {
		return newAuthError("revoked", "transaction revoked")
	}

	cacheExpiry := time.Now().Add(tokenCacheTTL)
	if expiresTime.Before(cacheExpiry) {
		cacheExpiry = expiresTime
	}
	tokenCache.Store(token, cachedToken{
		expiresAt: cacheExpiry,
		claimExp:  expiresTime,
	})

	return nil
}

// --- Trial token generation & verification ---

type trialPayload struct {
	DeviceID string `json:"did"`
	Exp      int64  `json:"exp"`
	Type     string `json:"typ"`
}

func generateTrialToken(deviceID string, expiresAt time.Time, typ string, secret []byte) string {
	payload := trialPayload{DeviceID: deviceID, Exp: expiresAt.Unix(), Type: typ}
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadBytes)
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payloadB64 + "." + sigB64
}

func verifyTrialToken(token string, secret []byte) error {
	if cached, ok := tokenCache.Load(token); ok {
		entry := cached.(cachedToken)
		if time.Now().Before(entry.expiresAt) && time.Now().Before(entry.claimExp) {
			return nil
		}
		tokenCache.Delete(token)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid trial token format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadBytes)
	if !hmac.Equal(sigBytes, mac.Sum(nil)) {
		return fmt.Errorf("invalid signature")
	}

	var payload trialPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	tokenExp := time.Unix(payload.Exp, 0)
	if time.Now().After(tokenExp) {
		return newAuthError("expired_trial", fmt.Sprintf("trial expired at %s", tokenExp.Format(time.RFC3339)))
	}

	cacheExpiry := time.Now().Add(tokenCacheTTL)
	if tokenExp.Before(cacheExpiry) {
		cacheExpiry = tokenExp
	}
	tokenCache.Store(token, cachedToken{
		expiresAt: cacheExpiry,
		claimExp:  tokenExp,
	})

	return nil
}

// --- Trial endpoint ---

func handleTrial(w http.ResponseWriter, r *http.Request, store *trialStore, secret []byte, duration time.Duration) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if len(secret) == 0 {
		http.Error(w, `{"error":"trial not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	deviceID := strings.ToUpper(strings.TrimSpace(req.DeviceID))
	if len(deviceID) != 36 || strings.Count(deviceID, "-") != 4 {
		http.Error(w, `{"error":"invalid device_id: expected UUID format"}`, http.StatusBadRequest)
		return
	}

	if record, exists := store.getTrial(deviceID); exists {
		if time.Now().Before(record.ExpiresAt) {
			token := generateTrialToken(deviceID, record.ExpiresAt, record.TokenType, secret)
			log.Printf("Trial re-issued: device=%s expires=%s", deviceID, record.ExpiresAt.Format(time.RFC3339))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"token":      token,
				"expires_at": record.ExpiresAt.Format(time.RFC3339),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "trial_already_used",
			"message": "Your free trial has expired. Subscribe in the Diction app to continue.",
		})
		return
	}

	expiresAt := time.Now().Add(duration)
	store.grantTrial(deviceID, expiresAt, "trial")
	token := generateTrialToken(deviceID, expiresAt, "trial", secret)

	log.Printf("Trial granted: device=%s expires=%s", deviceID, expiresAt.Format(time.RFC3339))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

// --- Auth middleware ---

type authErrorResponse struct {
	Error   string `json:"error"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

var authMessages = map[string]string{
	"missing_token":        "Diction Cloud requires an active subscription.",
	"expired_subscription": "Your subscription has expired. Please renew in the Diction app.",
	"revoked":              "Your subscription was revoked. Please resubscribe in the Diction app.",
	"expired_trial":        "Your free trial has expired. Subscribe in the Diction app to continue.",
	"invalid_token":        "Could not verify your subscription. Please reopen the Diction app and try again.",
}

func writeAuthError(w http.ResponseWriter, reason string) {
	msg, ok := authMessages[reason]
	if !ok {
		msg = authMessages["invalid_token"]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(authErrorResponse{
		Error:   "unauthorized",
		Reason:  reason,
		Message: msg,
	})
}

func authMiddleware(next http.HandlerFunc, enabled bool, bundleID string, trialSecret []byte) http.HandlerFunc {
	if !enabled {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeAuthError(w, "missing_token")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if token == "" {
			writeAuthError(w, "missing_token")
			return
		}

		var verifyErr error
		switch strings.Count(token, ".") {
		case 1:
			if len(trialSecret) == 0 {
				writeAuthError(w, "invalid_token")
				return
			}
			verifyErr = verifyTrialToken(token, trialSecret)
		case 2:
			verifyErr = verifyAppleJWS(token, bundleID)
		default:
			writeAuthError(w, "invalid_token")
			return
		}

		if verifyErr != nil {
			log.Printf("Token verification failed: %v", verifyErr)
			reason := "invalid_token"
			if ae, ok := verifyErr.(*authError); ok {
				reason = ae.reason
			}
			writeAuthError(w, reason)
			return
		}

		next(w, r)
	}
}

// textRoutesMiddleware returns a middleware that enforces the fail-closed guard
// for /v1/text/* routes (R12):
//   - AUTH_ENABLED=false AND TEXT_ROUTES_OPEN=false => 403 {"error":"text_routes_closed"}
//   - AUTH_ENABLED=false AND TEXT_ROUTES_OPEN=true  => open (no auth)
//   - AUTH_ENABLED=true                             => standard authMiddleware
//
// The guard is a deliberate speed bump that forces one informed decision rather
// than a security control. It becomes a real control the moment GH #14 lands.
func textRoutesMiddleware(authEnabled, routesOpen bool, bundleID string, trialSecret []byte) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		if authEnabled {
			return authMiddleware(next, true, bundleID, trialSecret)
		}
		if !routesOpen {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"text_routes_closed","hint":"Set TEXT_ROUTES_OPEN=true or AUTH_ENABLED=true to enable /v1/text/* routes. See AGENTS.md."}`)) //nolint:errcheck
			}
		}
		return next
	}
}

// withGatewayKey layers the pairing key (see gateway/pairing) around another
// auth middleware: a valid paired key (current or grace) bypasses it entirely;
// without one the request falls through unchanged, except in required mode
// where keyless requests are rejected with 401 invalid_key. This is what makes
// the paired key a real access control while keeping AUTH_ENABLED (JWS/trial)
// and TEXT_ROUTES_OPEN semantics intact for everyone else.
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
	authEnabled := core.EnvBoolOrDefault("AUTH_ENABLED", false)
	bundleID := core.EnvOrDefault("BUNDLE_ID", "one.diction")

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

	// Trial token config
	var trialSecret []byte
	if secretHex := core.EnvOrDefault("TRIAL_SECRET", ""); secretHex != "" {
		var err error
		trialSecret, err = hex.DecodeString(secretHex)
		if err != nil {
			return nil, "", fmt.Errorf("TRIAL_SECRET: invalid hex: %w", err)
		}
	}
	trialDBPath := core.EnvOrDefault("TRIAL_DB_PATH", "/data/trials.json")
	trialDuration, err := time.ParseDuration(core.EnvOrDefault("TRIAL_DURATION", "24h"))
	if err != nil {
		return nil, "", fmt.Errorf("TRIAL_DURATION: %w", err)
	}
	trials := newTrialStore(trialDBPath)

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
		inner := func(ctx context.Context, transcript, contextJSON, intent string) (string, string, error) {
			result, err := llm.processWithIntent(ctx, transcript, contextJSON, intent)
			return result, "", err
		}
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
		textRoutes:  llm.Enabled && (textRoutesOpen || pairingMode == pairing.ModeRequired),
		pairing:     keyStore != nil,
		keyRotation: keyStore != nil && !keyStore.Pinned(),
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

	audioMW := withGatewayKey(keyStore, pairingMode, func(next http.HandlerFunc) http.HandlerFunc {
		return authMiddleware(next, authEnabled, bundleID, trialSecret)
	})
	textMW := withGatewayKey(keyStore, pairingMode,
		textRoutesMiddleware(authEnabled, textRoutesOpen, bundleID, trialSecret))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", gw.HealthHandler())
	mux.HandleFunc("/v1/models", withCapabilities(gw.ModelsHandler(), capsForRequest))
	mux.HandleFunc("/v1/trial", func(w http.ResponseWriter, r *http.Request) {
		handleTrial(w, r, trials, trialSecret, trialDuration)
	})
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
	log.Printf("Diction Gateway starting on :%s (default_model=%s, auth=%v, trial=%v, llm=%v, text_routes=%v, pairing=%s, enhance_ms=%d, live_enhance_ms=%d)", port, defaultModel, authEnabled, len(trialSecret) > 0, llm.Enabled, textRoutesOpen, pairingMode, enhanceTimeoutMs, liveEnhanceTimeoutMs)
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
