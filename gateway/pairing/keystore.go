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

// ErrKeyPinned is returned by Rotate when the key comes from
// DICTION_GATEWAY_KEY: an env-pinned key has no keyring to rotate within.
var ErrKeyPinned = errors.New("gateway key is pinned via DICTION_GATEWAY_KEY")

// maxPreviousKeys bounds keyring growth if clients rotate aggressively. With
// 24h rotation coalescing (rotateCoalesce) a 30-day grace window holds at
// most ~30 retired secrets, so 40 means time-based grace pruning — the
// correct mechanism — is always what removes an entry, never truncation.
const maxPreviousKeys = 40

// rotateCoalesce: a Rotate() call within this long of the current secret's
// mint time does not create a new secret — it returns a freshly minted
// token from the unchanged current secret instead. Without this, N devices
// each rotating on their own 7-day cadence force N secret rotations
// clustered together, which used to truncate in-grace secrets out of the
// ring early (see the eviction regression test) and let a compromised
// device churn the ring to evict other devices' secrets.
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

// KeyStore holds the gateway pairing secret plus retired secrets still
// inside their grace window, and the token TTL policy applied at mint and
// verify time. Grace secrets stay valid on every route so a second device
// that was offline across a rotation keeps working until it exchanges for
// the current secret.
type KeyStore struct {
	mu     sync.Mutex
	path   string
	grace  time.Duration
	ttl    time.Duration
	pinned bool
	ring   keyringFile
	// now is overridden in tests to defeat rotation coalescing and to
	// exercise expiry without a real sleep.
	now func() time.Time
}

// LoadOrCreate returns the gateway keyring. When envKey is non-empty
// (DICTION_GATEWAY_KEY) it becomes the current key and rotation is
// disabled; retired keys from the file are still honored for their grace
// window. Otherwise the keyring is loaded from path, or generated and
// persisted on first start. ttl is the token TTL policy (0 = no expiry);
// it does not apply in pinned mode (a pinned literal has no expiry field).
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
	data, err := os.ReadFile(path)
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

// CurrentKey returns the active pairing secret. In pinned mode
// (DICTION_GATEWAY_KEY) this IS the bearer credential. Otherwise it is the
// signing secret used to mint per-device tokens — never hand this value
// itself to a client (over HTTP, in the QR, anywhere); use IssueToken.
func (ks *KeyStore) CurrentKey() string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.ring.Current.Key
}

// IssueToken returns a credential safe to hand to a device: the pinned key
// unchanged when DICTION_GATEWAY_KEY is set (an operator who fixed a
// manual key wants exactly that key, not a freshly minted one — and TTL
// does not apply to it), otherwise a new token signed against the current
// secret with the configured TTL. Every call mints a distinct token —
// pairing N devices yields N different QR codes — but any still-valid
// token keeps verifying, so there is nothing per-device to persist and no
// risk from calling this repeatedly.
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

// Fingerprint is the loggable identity of the current key: prefix + first 8
// chars of the random part. Never log the full key.
func (ks *KeyStore) Fingerprint() string {
	key := ks.CurrentKey()
	trimmed := strings.TrimPrefix(key, tokenPrefix)
	if len(trimmed) > 8 {
		trimmed = trimmed[:8]
	}
	return tokenPrefix + trimmed + "…"
}

// Verify reports whether token is valid: in pinned mode an exact match on
// the fixed key (TTL never applies to a pinned literal); otherwise a token
// bearing a correct HMAC tag under the current secret or a retired secret
// still inside its grace window, honoring that token's own embedded
// expiry — plus, only while no TTL is configured, a legacy exact match
// against the raw secret itself (the pre-per-device-token credential; see
// the removal trigger in gatewaykey.go's history — deleted in the first
// release after v13 ships). hmac.Equal and ConstantTimeCompare are both
// constant-time; a garbage token still walks the full grace ring before
// failing, so verification time does not distinguish "expired" from
// "never valid" at the network level (accepted; see plan Risks).
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
	// Legacy acceptance: the pre-per-device-token shared secret still works
	// as a bearer token in its own right, but only while TTL is unset — a
	// credential that can never expire must not survive the operator
	// opting into expiry (Expiry policy rule 3).
	if ttl == 0 {
		if subtle.ConstantTimeCompare([]byte(token), []byte(current)) == 1 {
			return true
		}
		for _, prev := range previous {
			if prev.RetiredAt.Before(cutoff) {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(prev.Key)) == 1 {
				return true
			}
		}
	}
	log.Printf("pairing: token rejected (nonce %s, verdict %s)", tokenNoncePrefix(token), worst)
	return false
}

// Rotate retires the current secret into the grace ring and generates a
// new one — unless the current secret is younger than rotateCoalesce, in
// which case it does nothing to the ring and returns the current secret
// with coalesced=true and a zero previousValidUntil (nothing was
// retired). Either way the caller should mint a fresh token via
// IssueToken for its response; Rotate itself only manages the secret.
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

// KeyStoreFromEnv builds the keyring from DICTION_GATEWAY_KEY,
// DICTION_KEY_PATH, DICTION_KEY_GRACE and DICTION_TOKEN_TTL. Returns nil
// (no error) when pairing is off, so callers can wire
// `ks := pairing.KeyStoreFromEnv(mode)` unconditionally.
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
		// A malformed value is a hard startup error, not a silent fallback
		// to "no expiry" — silently disabling a security control the
		// operator asked for is the worse failure.
		ttl, err = time.ParseDuration(ttlRaw)
		if err != nil {
			return nil, fmt.Errorf("DICTION_TOKEN_TTL: %w", err)
		}
	}

	envKey := core.EnvOrDefault("DICTION_GATEWAY_KEY", "")

	if ttl > 0 {
		// Clock sanity: a Pi without an RTC boots at epoch and would mint
		// tokens that are nonsense (already expired, or valid for
		// decades). Warning only, never fatal — the clock usually
		// corrects via NTP moments later.
		if time.Now().Before(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
			fmt.Printf("\033[33mwarning: system clock reads before 2026-01-01 with DICTION_TOKEN_TTL set — minted tokens may have a nonsense expiry until the clock corrects\033[0m\n")
		}
		// Enforcement sanity: expiring tokens protect nothing if keyless
		// requests are accepted anyway. This is the misconfiguration most
		// likely to void the whole feature silently.
		if mode != ModeRequired {
			fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL is set but DICTION_GATEWAY_AUTH=%s — tokens will expire, but keyless requests are still accepted, so the expiry protects nothing until DICTION_GATEWAY_AUTH=required\033[0m\n", mode)
		}
		if envKey != "" {
			fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL is set but DICTION_GATEWAY_KEY pins a literal key — TTL does not apply to a pinned key and is inert\033[0m\n")
		} else {
			fmt.Printf("\033[33mwarning: DICTION_TOKEN_TTL=%s — legacy and never-expiring tokens are rejected; previously paired devices re-pair once\033[0m\n", ttl)
		}
	}

	return LoadOrCreate(
		core.EnvOrDefault("DICTION_KEY_PATH", "/data/gateway-keys.json"),
		envKey, grace, ttl)
}

// WarnDeprecatedEnv prints an orange startup line when DICTION_GATEWAY_KEY
// is set: the var is deprecated — still fully honored, but it disables
// both key rotation and token TTL. Uses raw fmt (not log), matching
// PrintQR's convention, so it renders cleanly in `docker compose logs`.
func WarnDeprecatedEnv() {
	if core.EnvOrDefault("DICTION_GATEWAY_KEY", "") == "" {
		return
	}
	fmt.Print("\033[33mwarning: DICTION_GATEWAY_KEY is deprecated — still honored, but it disables both key rotation and token TTL. Migrate by unsetting it and re-pairing via QR.\033[0m\n")
}
