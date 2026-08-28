package core

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// PairingMode is the value of DICTION_GATEWAY_AUTH.
type PairingMode string

const (
	// PairingOff disables the gateway key entirely: no key generated, no QR,
	// /v1/auth/* not registered.
	PairingOff PairingMode = "off"
	// PairingOptional generates the key, prints the QR and accepts the key,
	// but keyless requests still pass. The upgrade-safe default.
	PairingOptional PairingMode = "optional"
	// PairingRequired rejects requests that do not carry a valid gateway key.
	PairingRequired PairingMode = "required"
)

// PairingModeFromEnv parses DICTION_GATEWAY_AUTH, defaulting to optional so
// image upgrades never lock out existing deploys.
func PairingModeFromEnv() PairingMode {
	switch strings.ToLower(EnvOrDefault("DICTION_GATEWAY_AUTH", "optional")) {
	case "off", "false", "0", "disabled":
		return PairingOff
	case "required", "require", "enforce":
		return PairingRequired
	default:
		return PairingOptional
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

// GatewayKeyMiddleware guards data routes with the gateway pairing key.
// A matching key (current or grace) always passes. Without one, requests pass
// in optional mode and get 401 reason "invalid_key" in required mode.
//
// Community builds compose this FIRST; when the key does not match and
// AUTH_ENABLED=true, the request falls through to the standard auth middleware,
// so manually configured keys, trial tokens and JWS all keep working.
func GatewayKeyMiddleware(ks *KeyStore, required bool, next http.HandlerFunc) http.HandlerFunc {
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
			"This gateway requires pairing. Scan the QR code shown in the gateway logs.")
	}
}

// RegisterPairingRoutes registers /v1/auth/key and /v1/auth/rotate. Both are
// authenticated with any still-valid gateway key (current or grace) — that is
// how a device holding a retired key silently catches up after a rotation.
// Call only when mode != off; cloud builds never call it, so the routes 404.
func RegisterPairingRoutes(mux *http.ServeMux, ks *KeyStore) {
	requireKey := func(w http.ResponseWriter, r *http.Request) bool {
		if ks.Verify(bearerToken(r)) {
			return true
		}
		writePairingError(w, http.StatusUnauthorized, "invalid_key",
			"Pairing key not recognized. Re-scan the QR code shown in the gateway logs.")
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
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"key":      ks.CurrentKey(),
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
		newKey, validUntil, err := ks.Rotate()
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
		log.Printf("gateway key rotated (new fingerprint %s, previous valid until %s)",
			ks.Fingerprint(), validUntil.UTC().Format(time.RFC3339))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"key":                  newKey,
			"previous_valid_until": validUntil.UTC().Format(time.RFC3339),
		})
	})
}

// VerifyRequest reports whether the request carries a valid gateway key as its
// Bearer token. Nil-safe so callers can compose it unconditionally. Exported
// for community main builds that must layer the key check around their own
// auth middleware (a matching key bypasses it; see GatewayKeyMiddleware).
func (ks *KeyStore) VerifyRequest(r *http.Request) bool {
	if ks == nil {
		return false
	}
	return ks.Verify(bearerToken(r))
}

// WritePairingRequired writes the 401 invalid_key rejection used when
// DICTION_GATEWAY_AUTH=required refuses a keyless request.
func WritePairingRequired(w http.ResponseWriter) {
	writePairingError(w, http.StatusUnauthorized, "invalid_key",
		"This gateway requires pairing. Scan the QR code shown in the gateway logs.")
}
