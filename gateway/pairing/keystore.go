package pairing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DictionLabs/Diction/gateway/core"
)

// ErrKeyPinned is returned by Rotate when the key is pinned via DICTION_GATEWAY_KEY.
var ErrKeyPinned = errors.New("gateway key is pinned via DICTION_GATEWAY_KEY")

// maxPreviousKeys bounds the ring so time-based grace pruning, not truncation, removes entries.
const maxPreviousKeys = 40

// rotateCoalesce: a Rotate this soon after minting keeps the secret, stopping ring churn.
const rotateCoalesce = 24 * time.Hour

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

// KeyStore holds the pairing secret, retired secrets still in their grace window, and the TTL.
type KeyStore struct {
	mu     sync.Mutex
	path   string
	grace  time.Duration
	ttl    time.Duration
	pinned bool
	ring   keyringFile
	// now is overridden in tests to skip rotation coalescing and real sleeps.
	now func() time.Time
}

// LoadOrCreate returns the keyring from envKey (pinned) or the file at path, else a new one.
func LoadOrCreate(path, envKey string, grace, ttl time.Duration) (*KeyStore, error) {
	ks := &KeyStore{path: path, grace: grace, ttl: ttl, now: time.Now}
	ks.ring = loadKeyringFile(path)
	if envKey != "" {
		ks.pinned = true
		ks.ring.Current = keyRecord{Key: envKey, CreatedAt: time.Now().UTC()}
		return ks, nil
	}
	if ks.ring.Current.Key == "" {
		key, err := newGatewaySecret()
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
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return keyringFile{}
	}
	if json.Unmarshal(data, &ring) != nil {
		return keyringFile{}
	}
	return ring
}

func newGatewaySecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate gateway secret: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// CurrentKey returns the active secret; never hand it to a client unless pinned (use IssueToken).
func (ks *KeyStore) CurrentKey() string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.ring.Current.Key
}

// IssueToken returns a credential safe for a device: the pinned key, else a freshly minted token.
func (ks *KeyStore) IssueToken() (string, error) {
	if ks.pinned {
		return ks.CurrentKey(), nil
	}
	ks.mu.Lock()
	ttl := ks.ttl
	ks.mu.Unlock()
	return signToken(ks.CurrentKey(), ttl)
}

// Pinned reports whether the key came from DICTION_GATEWAY_KEY.
func (ks *KeyStore) Pinned() bool { return ks.pinned }

// Fingerprint is the loggable identity of the current key; never log the full key.
func (ks *KeyStore) Fingerprint() string {
	key := ks.CurrentKey()
	trimmed := strings.TrimPrefix(key, tokenPrefix)
	if len(trimmed) > 8 {
		trimmed = trimmed[:8]
	}
	return tokenPrefix + trimmed + "…"
}

// Verify reports whether token is valid: exact match if pinned, else an in-grace HMAC match.
func (ks *KeyStore) Verify(token string) bool {
	if token == "" {
		return false
	}
	ks.mu.Lock()
	if ks.pinned {
		current := ks.ring.Current.Key
		ks.mu.Unlock()
		return subtle.ConstantTimeCompare([]byte(token), []byte(current)) == 1
	}
	ttl := ks.ttl
	current := ks.ring.Current.Key
	previous := append([]retiredKey(nil), ks.ring.Previous...)
	now := ks.now()
	ks.mu.Unlock()

	verdict := verifyToken(token, current, ttl, now)
	if verdict == verdictOK {
		return true
	}
	worst := verdict
	cutoff := now.Add(-ks.grace)
	for _, prev := range previous {
		if prev.RetiredAt.Before(cutoff) {
			continue
		}
		v := verifyToken(token, prev.Key, ttl, now)
		if v == verdictOK {
			return true
		}
		if v == verdictExpired {
			worst = verdictExpired
		}
	}
	log.Printf("pairing: token rejected (nonce %s, verdict %s)", tokenNoncePrefix(token), worst)
	return false
}

// Rotate retires the current secret into the grace ring unless it is younger than rotateCoalesce.
func (ks *KeyStore) Rotate() (newKey string, previousValidUntil time.Time, coalesced bool, err error) {
	if ks.pinned {
		return "", time.Time{}, false, ErrKeyPinned
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	now := ks.now()
	if age := now.Sub(ks.ring.Current.CreatedAt); age < rotateCoalesce {
		log.Printf("pairing: rotation coalesced — current secret is %s old", age.Round(time.Second))
		return ks.ring.Current.Key, time.Time{}, true, nil
	}
	key, err := newGatewaySecret()
	if err != nil {
		return "", time.Time{}, false, err
	}
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
		return "", time.Time{}, false, err
	}
	return key, now.Add(ks.grace), false, nil
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

// KeyStoreFromEnv builds the keyring from the DICTION_* env vars; nil, nil when pairing is off.
func KeyStoreFromEnv(mode Mode) (*KeyStore, error) {
	if mode == ModeOff {
		return nil, nil
	}
	grace, err := time.ParseDuration(core.EnvOrDefault("DICTION_KEY_GRACE", "720h"))
	if err != nil {
		return nil, fmt.Errorf("DICTION_KEY_GRACE: %w", err)
	}
	ttlRaw := core.EnvOrDefault("DICTION_TOKEN_TTL", "")
	var ttl time.Duration
	if ttlRaw != "" {
		// A malformed value fails startup: silently dropping the requested expiry is worse.
		ttl, err = time.ParseDuration(ttlRaw)
		if err != nil {
			return nil, fmt.Errorf("DICTION_TOKEN_TTL: %w", err)
		}
	}

	envKey := core.EnvOrDefault("DICTION_GATEWAY_KEY", "")
	warnTTLConfig(ttl, mode, envKey)

	return LoadOrCreate(
		core.EnvOrDefault("DICTION_KEY_PATH", "/data/gateway-keys.json"),
		envKey, grace, ttl)
}

func warnTTLConfig(ttl time.Duration, mode Mode, envKey string) {
	if ttl <= 0 {
		return
	}
	// A Pi without an RTC boots at epoch and mints nonsense expiries; warn only, NTP fixes it.
	if time.Now().Before(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		fmt.Printf("\033[33mwarning: system clock reads before 2026-01-01 with DICTION_TOKEN_TTL set — minted tokens may have a nonsense expiry until the clock corrects\033[0m\n")
	}
	// Expiring tokens protect nothing while keyless requests are accepted.
	if mode != ModeRequired {
		fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL is set but DICTION_GATEWAY_AUTH=%s — tokens will expire, but keyless requests are still accepted, so the expiry protects nothing until DICTION_GATEWAY_AUTH=required\033[0m\n", mode)
	}
	if envKey != "" {
		fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL is set but DICTION_GATEWAY_KEY pins a literal key — TTL does not apply to a pinned key and is inert\033[0m\n")
	} else {
		fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL=%s — legacy and never-expiring tokens are rejected; previously paired devices re-pair once\033[0m\n", ttl)
	}
}

// WarnDeprecatedEnv prints an orange startup line when DICTION_GATEWAY_KEY is set (deprecated).
func WarnDeprecatedEnv() {
	if core.EnvBoolOrDefault("AUTH_ENABLED", false) {
		fmt.Print("\033[33mwarning: AUTH_ENABLED is retired — treated as DICTION_GATEWAY_AUTH=required. Pair the app by scanning the QR (docker exec <container> gateway auth), then replace AUTH_ENABLED with DICTION_GATEWAY_AUTH: required.\033[0m\n")
	}
	if core.EnvOrDefault("DICTION_GATEWAY_KEY", "") == "" {
		return
	}
	fmt.Print("\033[33mwarning: DICTION_GATEWAY_KEY is deprecated — still honored, but it disables both key rotation and token TTL. Migrate by unsetting it and re-pairing via QR.\033[0m\n")
}
