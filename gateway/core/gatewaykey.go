package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Gateway keys are the self-hosted pairing credential: `dk_` + base64url(32
// random bytes). Zero dots, so the cloud auth middleware's dot-count
// discriminator (1 dot = trial HMAC, 2 dots = Apple JWS) can never confuse one
// for its own tokens.
const gatewayKeyPrefix = "dk_"

// ErrKeyPinned is returned by Rotate when the key comes from
// DICTION_GATEWAY_KEY: an env-pinned key has no keyring to rotate within.
var ErrKeyPinned = errors.New("gateway key is pinned via DICTION_GATEWAY_KEY")

// maxPreviousKeys bounds keyring growth if clients rotate aggressively.
const maxPreviousKeys = 10

type keyRecord struct {
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

type retiredKey struct {
	Key       string    `json:"key"`
	RetiredAt time.Time `json:"retired_at"`
}

type keyringFile struct {
	Current  keyRecord    `json:"current"`
	Previous []retiredKey `json:"previous"`
}

// KeyStore holds the gateway pairing key plus retired keys still inside their
// grace window. Grace keys stay valid on every route so a second device that
// was offline across a rotation keeps working until it exchanges for the
// current key.
type KeyStore struct {
	mu     sync.Mutex
	path   string
	grace  time.Duration
	pinned bool
	ring   keyringFile
}

// LoadOrCreateKeyStore returns the gateway keyring. When envKey is non-empty
// (DICTION_GATEWAY_KEY) it becomes the current key and rotation is disabled;
// retired keys from the file are still honored for their grace window. Otherwise
// the keyring is loaded from path, or generated and persisted on first start.
func LoadOrCreateKeyStore(path, envKey string, grace time.Duration) (*KeyStore, error) {
	ks := &KeyStore{path: path, grace: grace}
	ks.ring = loadKeyringFile(path)
	if envKey != "" {
		ks.pinned = true
		ks.ring.Current = keyRecord{Key: envKey, CreatedAt: time.Now().UTC()}
		return ks, nil
	}
	if ks.ring.Current.Key == "" {
		key, err := newGatewayKey()
		if err != nil {
			return nil, err
		}
		ks.ring.Current = keyRecord{Key: key, CreatedAt: time.Now().UTC()}
		if err := ks.persistLocked(); err != nil {
			return nil, err
		}
	}
	return ks, nil
}

func loadKeyringFile(path string) keyringFile {
	var ring keyringFile
	data, err := os.ReadFile(path)
	if err != nil {
		return keyringFile{}
	}
	if json.Unmarshal(data, &ring) != nil {
		return keyringFile{}
	}
	return ring
}

func newGatewayKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate gateway key: %w", err)
	}
	return gatewayKeyPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// CurrentKey returns the active pairing key.
func (ks *KeyStore) CurrentKey() string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.ring.Current.Key
}

// Pinned reports whether the key came from DICTION_GATEWAY_KEY.
func (ks *KeyStore) Pinned() bool { return ks.pinned }

// Fingerprint is the loggable identity of the current key: prefix + first 8
// chars of the random part. Never log the full key.
func (ks *KeyStore) Fingerprint() string {
	key := ks.CurrentKey()
	trimmed := strings.TrimPrefix(key, gatewayKeyPrefix)
	if len(trimmed) > 8 {
		trimmed = trimmed[:8]
	}
	return gatewayKeyPrefix + trimmed + "…"
}

// Verify reports whether token matches the current key or a retired key still
// inside its grace window. Constant-time per comparison.
func (ks *KeyStore) Verify(token string) bool {
	if token == "" {
		return false
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ok := subtle.ConstantTimeCompare([]byte(token), []byte(ks.ring.Current.Key)) == 1
	cutoff := time.Now().Add(-ks.grace)
	for _, prev := range ks.ring.Previous {
		if prev.RetiredAt.Before(cutoff) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(prev.Key)) == 1 {
			ok = true
		}
	}
	return ok
}

// Rotate retires the current key into the grace ring and generates a new one.
// Returns the new key and how long the retired key remains valid.
func (ks *KeyStore) Rotate() (newKey string, previousValidUntil time.Time, err error) {
	if ks.pinned {
		return "", time.Time{}, ErrKeyPinned
	}
	key, err := newGatewayKey()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.ring.Previous = append([]retiredKey{{Key: ks.ring.Current.Key, RetiredAt: now}}, ks.ring.Previous...)
	cutoff := now.Add(-ks.grace)
	pruned := ks.ring.Previous[:0]
	for _, prev := range ks.ring.Previous {
		if prev.RetiredAt.Before(cutoff) {
			continue
		}
		pruned = append(pruned, prev)
	}
	if len(pruned) > maxPreviousKeys {
		pruned = pruned[:maxPreviousKeys]
	}
	ks.ring.Previous = pruned
	ks.ring.Current = keyRecord{Key: key, CreatedAt: now}
	if err := ks.persistLocked(); err != nil {
		return "", time.Time{}, err
	}
	return key, now.Add(ks.grace), nil
}

// persistLocked writes the keyring atomically (tmp + rename). Callers hold mu.
func (ks *KeyStore) persistLocked() error {
	data, err := json.MarshalIndent(ks.ring, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ks.path), 0o700); err != nil {
		return err
	}
	tmp := ks.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ks.path)
}

// KeyStoreFromEnv builds the keyring from DICTION_GATEWAY_KEY, DICTION_KEY_PATH
// and DICTION_KEY_GRACE. Returns nil (no error) when pairing is off, so callers
// can wire `ks := core.KeyStoreFromEnv(mode)` unconditionally.
func KeyStoreFromEnv(mode PairingMode) (*KeyStore, error) {
	if mode == PairingOff {
		return nil, nil
	}
	grace, err := time.ParseDuration(EnvOrDefault("DICTION_KEY_GRACE", "720h"))
	if err != nil {
		return nil, fmt.Errorf("DICTION_KEY_GRACE: %w", err)
	}
	return LoadOrCreateKeyStore(
		EnvOrDefault("DICTION_KEY_PATH", "/data/gateway-keys.json"),
		EnvOrDefault("DICTION_GATEWAY_KEY", ""),
		grace)
}
