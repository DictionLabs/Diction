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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
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

// --- Main ---

// buildMux reads configuration from environment variables, wires up all
// handlers, and returns the HTTP mux and the port to listen on.
// Extracted from main() to allow testing without starting a real server.
func buildMux() (http.Handler, string, error) {
	port := core.EnvOrDefault("GATEWAY_PORT", "8080")
	defaultModel := core.EnvOrDefault("DEFAULT_MODEL", "small")
	maxBodySize := int64(core.EnvIntOrDefault("MAX_BODY_SIZE", 209715200))
	authEnabled := core.EnvBoolOrDefault("AUTH_ENABLED", false)
	bundleID := core.EnvOrDefault("BUNDLE_ID", "one.diction")

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
	var postProcess func(context.Context, string, string, string) (string, string, error)
	if llm.Enabled {
		postProcess = func(ctx context.Context, transcript, contextJSON, intent string) (string, string, error) {
			result, err := llm.processWithIntent(ctx, transcript, contextJSON, intent)
			return result, "", err
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", gw.HealthHandler())
	mux.HandleFunc("/v1/models", withCapabilities(gw.ModelsHandler(), llm.Enabled, textRoutesOpen))
	mux.HandleFunc("/v1/trial", func(w http.ResponseWriter, r *http.Request) {
		handleTrial(w, r, trials, trialSecret, trialDuration)
	})
	mux.HandleFunc("/v1/audio/transcriptions", authMiddleware(
		gw.TranscriptionHandlerWithPostProcess(postProcess), authEnabled, bundleID, trialSecret,
	))
	mux.HandleFunc("/v1/audio/stream", authMiddleware(
		gw.StreamingHandlerWithPostProcess(postProcess), authEnabled, bundleID, trialSecret,
	))
	textMW := textRoutesMiddleware(authEnabled, textRoutesOpen, bundleID, trialSecret)
	mux.HandleFunc("/v1/text/process", textMW(handleTextProcess(llm)))
	mux.HandleFunc("/v1/text/suggest", textMW(handleTextSuggest(llm)))
	mux.HandleFunc("/v1/text/summarize", textMW(handleTextSummarize(llm)))
	mux.HandleFunc("/", gw.CatchAllHandler())

	log.Printf("Diction Gateway starting on :%s (default_model=%s, auth=%v, trial=%v, llm=%v, text_routes=%v)", port, defaultModel, authEnabled, len(trialSecret) > 0, llm.Enabled, textRoutesOpen)
	return mux, port, nil
}

func main() {
	mux, port, err := buildMux()
	if err != nil {
		log.Fatalf("%v", err)
	}
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
