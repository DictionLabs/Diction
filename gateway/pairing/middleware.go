package pairing

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
)

// Mode is the value of DICTION_GATEWAY_AUTH.
type Mode string

const (
	// ModeOff disables the gateway key entirely: no key generated, no QR,
	// /v1/auth/* not registered.
	ModeOff Mode = "off"
	// ModeOptional generates the key, prints the QR and accepts the key,
	// but keyless requests still pass. The upgrade-safe default.
	ModeOptional Mode = "optional"
	// ModeRequired rejects requests that do not carry a valid gateway key.
	ModeRequired Mode = "required"
)

// ModeFromEnv parses DICTION_GATEWAY_AUTH, defaulting to optional so image
// upgrades never lock out existing deploys.
func ModeFromEnv() Mode {
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

func writePairingError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":   "unauthorized",
		"reason":  reason,
		"message": message,
	})
}

// KeyMiddleware guards data routes with the gateway pairing key. A matching
// key (current or grace, unexpired) always passes. Without one, requests
// pass in optional mode and get 401 reason "invalid_key" in required mode
// — the same response for an expired token, a garbage token, or no token
// at all (Expiry policy rule 1: no refresh-token semantics means there is
// nothing a client should do differently, only re-pair).
//
// Community builds compose this FIRST; when the key does not match and
// AUTH_ENABLED=true, the request falls through to the standard auth middleware,
// so manually configured keys, trial tokens and JWS all keep working.
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

// RegisterRoutes registers /v1/auth/key and /v1/auth/rotate. Both are
// authenticated with any still-valid gateway key (current or grace,
// unexpired) — that is how a device holding a retired key silently
// catches up after a rotation. Call only when mode != off; cloud builds
// never call it, so the routes 404.
func RegisterRoutes(mux *http.ServeMux, ks *KeyStore) {
	requireKey := func(w http.ResponseWriter, r *http.Request) bool {
		if ks.Verify(bearerToken(r)) {
			return true
		}
		writePairingError(w, http.StatusUnauthorized, "invalid_key",
			"Pairing key not recognized. Run 'docker exec <container> gateway auth' on your server to see the current QR code.")
		return false
	}

	mux.HandleFunc("/v1/auth/key", func(w http.ResponseWriter, r *http.Request) {
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
		json.NewEncoder(w).Encode(map[string]any{
			"key":      token,
			"rotation": !ks.Pinned(),
		})
	})

	mux.HandleFunc("/v1/auth/rotate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if !requireKey(w, r) {
			return
		}
		_, validUntil, coalesced, err := ks.Rotate()
		if err == ErrKeyPinned {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
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
		if coalesced {
			resp["coalesced"] = true
			log.Printf("gateway key rotation coalesced (fingerprint %s unchanged)", ks.Fingerprint())
		} else {
			resp["previous_valid_until"] = validUntil.UTC().Format(time.RFC3339)
			log.Printf("gateway key rotated (new fingerprint %s, previous valid until %s)",
				ks.Fingerprint(), validUntil.UTC().Format(time.RFC3339))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
}

// VerifyRequest reports whether the request carries a valid gateway key as its
// Bearer token. Nil-safe so callers can compose it unconditionally. Exported
// for community main builds that must layer the key check around their own
// auth middleware (a matching key bypasses it; see KeyMiddleware).
func (ks *KeyStore) VerifyRequest(r *http.Request) bool {
	if ks == nil {
		return false
	}
	return ks.Verify(bearerToken(r))
}

// WriteRequired writes the 401 invalid_key rejection used when
// DICTION_GATEWAY_AUTH=required refuses a keyless request.
func WriteRequired(w http.ResponseWriter) {
	writePairingError(w, http.StatusUnauthorized, "invalid_key",
		"This gateway requires pairing. Run 'docker exec <container> gateway auth' on your server to see the QR code again.")
}
