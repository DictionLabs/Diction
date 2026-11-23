package pairing

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
)

// Mode is the value of DICTION_GATEWAY_AUTH.
type Mode string

const (
	// ModeOff disables the gateway key entirely (no key, no QR, no /v1/auth/*).
	ModeOff Mode = "off"
	// ModeOptional generates and accepts the key, but keyless requests still pass.
	ModeOptional Mode = "optional"
	// ModeRequired rejects requests that do not carry a valid gateway key.
	ModeRequired Mode = "required"
)

// ModeFromEnv parses DICTION_GATEWAY_AUTH (default optional); retired AUTH_ENABLED=true = required.
func ModeFromEnv() Mode {
	if core.EnvBoolOrDefault("AUTH_ENABLED", false) {
		return ModeRequired
	}
	switch strings.ToLower(core.EnvOrDefault("DICTION_GATEWAY_AUTH", "optional")) {
	case "off", "false", "0", "disabled":
		return ModeOff
	case "required", "require", "enforce":
		return ModeRequired
	default:
		return ModeOptional
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func writePairingError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]string{
		"error":   "unauthorized",
		"reason":  reason,
		"message": message,
	})
}

// KeyMiddleware guards data routes: a valid key passes; keyless passes unless required (401).
func KeyMiddleware(ks *KeyStore, required bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ks != nil && ks.Verify(bearerToken(r)) {
			next(w, r)
			return
		}
		if !required {
			next(w, r)
			return
		}
		writePairingError(w, http.StatusUnauthorized, "invalid_key",
			"This gateway requires pairing. Run 'docker exec <container> gateway auth' on your server to see the QR code again.")
	}
}

// RegisterRoutes registers /v1/auth/key and /v1/auth/rotate; call only when mode != off.
func RegisterRoutes(mux *http.ServeMux, ks *KeyStore) {
	requireKey := func(w http.ResponseWriter, r *http.Request) bool {
		if ks.Verify(bearerToken(r)) {
			return true
		}
		writePairingError(w, http.StatusUnauthorized, "invalid_key",
			"Pairing key not recognized. Run 'docker exec <container> gateway auth' on your server to see the current QR code.")
		return false
	}
	mux.HandleFunc("/v1/auth/key", keyHandler(ks, requireKey))
	mux.HandleFunc("/v1/auth/rotate", rotateHandler(ks, requireKey))
}

type keyGuard func(w http.ResponseWriter, r *http.Request) bool

func keyHandler(ks *KeyStore, requireKey keyGuard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if !requireKey(w, r) {
			return
		}
		token, err := ks.IssueToken()
		if err != nil {
			http.Error(w, `{"error":"could not mint pairing token"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{
			"key":      token,
			"rotation": !ks.Pinned(),
		})
	}
}

func rotateHandler(ks *KeyStore, requireKey keyGuard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if !requireKey(w, r) {
			return
		}
		_, validUntil, coalesced, err := ks.Rotate()
		if errors.Is(err, ErrKeyPinned) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]string{
				"error":  "rotation_unavailable",
				"reason": "env_pinned",
			})
			return
		}
		if err != nil {
			log.Printf("gateway key rotation failed: %v", err)
			http.Error(w, `{"error":"rotation failed"}`, http.StatusInternalServerError)
			return
		}
		token, err := ks.IssueToken()
		if err != nil {
			http.Error(w, `{"error":"could not mint pairing token"}`, http.StatusInternalServerError)
			return
		}
		resp := map[string]any{"key": token}
		logRotation(ks, resp, coalesced, validUntil)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, resp)
	}
}

func logRotation(ks *KeyStore, resp map[string]any, coalesced bool, validUntil time.Time) {
	if coalesced {
		resp["coalesced"] = true
		log.Printf("gateway key rotation coalesced (fingerprint %s unchanged)", ks.Fingerprint())
		return
	}
	resp["previous_valid_until"] = validUntil.UTC().Format(time.RFC3339)
	log.Printf("gateway key rotated (new fingerprint %s, previous valid until %s)",
		ks.Fingerprint(), validUntil.UTC().Format(time.RFC3339))
}

// VerifyRequest reports whether the request carries a valid gateway key; nil-safe.
func (ks *KeyStore) VerifyRequest(r *http.Request) bool {
	if ks == nil {
		return false
	}
	return ks.Verify(bearerToken(r))
}

// WriteRequired writes the 401 invalid_key rejection for a keyless request in required mode.
func WriteRequired(w http.ResponseWriter) {
	writePairingError(w, http.StatusUnauthorized, "invalid_key",
		"This gateway requires pairing. Run 'docker exec <container> gateway auth' on your server to see the QR code again.")
}
