package pairing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestKeyStore(t *testing.T, envKey string, grace, ttl time.Duration) (*KeyStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway-keys.json")
	ks, err := LoadOrCreate(path, envKey, grace, ttl)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return ks, path
}

func TestKeyStoreGeneratesAndPersists(t *testing.T) {
	ks, path := newTestKeyStore(t, "", time.Hour, 0)
	key := ks.CurrentKey()
	if !strings.HasPrefix(key, "dk_") {
		t.Fatalf("key %q missing dk_ prefix", key)
	}
	if strings.Contains(key, ".") {
		t.Fatalf("key %q contains a dot; collides with trial/JWS discriminator", key)
	}
	if len(key) < 40 {
		t.Fatalf("key too short: %d chars", len(key))
	}
	ks2, err := LoadOrCreate(path, "", time.Hour, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ks2.CurrentKey() != key {
		t.Fatalf("reload produced a different key")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("keyring file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestKeyStoreCorruptFileRegenerates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-keys.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ks, err := LoadOrCreate(path, "", time.Hour, 0)
	if err != nil {
		t.Fatalf("LoadOrCreate on corrupt file: %v", err)
	}
	if !strings.HasPrefix(ks.CurrentKey(), "dk_") {
		t.Fatalf("no key regenerated after corrupt file")
	}
}

func TestKeyStoreEnvPinned(t *testing.T) {
	ks, path := newTestKeyStore(t, "dk_pinned_key_value", time.Hour, 0)
	if !ks.Pinned() {
		t.Fatal("expected pinned store")
	}
	if ks.CurrentKey() != "dk_pinned_key_value" {
		t.Fatalf("current = %q, want env key", ks.CurrentKey())
	}
	if _, _, _, err := ks.Rotate(); err != ErrKeyPinned {
		t.Fatalf("Rotate on pinned = %v, want ErrKeyPinned", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pinned key was persisted to disk")
	}
}

// TestKeyStorePinnedIgnoresTTL proves TTL never applies to a pinned literal:
// IssueToken returns the literal unchanged and Verify accepts it forever.
func TestKeyStorePinnedIgnoresTTL(t *testing.T) {
	ks, _ := newTestKeyStore(t, "dk_pinned_ttl_ignored", time.Hour, time.Millisecond)
	token, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	if token != "dk_pinned_ttl_ignored" {
		t.Fatalf("pinned IssueToken = %q, want the literal unchanged", token)
	}
	time.Sleep(5 * time.Millisecond)
	if !ks.Verify(token) {
		t.Fatal("pinned key must keep verifying regardless of TTL")
	}
}

func TestKeyStoreIssueTokenPerDevice(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		token, err := ks.IssueToken()
		if err != nil {
			t.Fatalf("IssueToken %d: %v", i, err)
		}
		if seen[token] {
			t.Fatalf("IssueToken returned a duplicate on call %d: %q", i, token)
		}
		seen[token] = true
		if !ks.Verify(token) {
			t.Fatalf("token %d must verify: %q", i, token)
		}
	}
}

func TestKeyStoreVerify(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	token, err := ks.IssueToken()
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if !ks.Verify(token) {
		t.Fatal("issued token must verify")
	}
	for _, bad := range []string{"", "dk_wrong", token + "x", token[:len(token)-1]} {
		if ks.Verify(bad) {
			t.Fatalf("token %q must not verify", bad)
		}
	}
}

func TestKeyStoreVerify_LegacySecretAcceptedOnlyWithoutTTL(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	secret := ks.CurrentKey()
	if !ks.Verify(secret) {
		t.Fatal("legacy raw-secret bearer must verify while TTL is unset")
	}

	ksStrict, _ := newTestKeyStore(t, "", time.Hour, time.Hour)
	strictSecret := ksStrict.CurrentKey()
	if ksStrict.Verify(strictSecret) {
		t.Fatal("legacy raw-secret bearer must be rejected once TTL is set")
	}
}

func TestKeyStoreVerify_NeverExpiringRejectedOnceTTLSet(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	token, err := ks.IssueToken() // minted while TTL unset: expiry==0 sentinel
	if err != nil {
		t.Fatal(err)
	}
	ks.mu.Lock()
	ks.ttl = time.Hour // operator enables TTL on an existing deploy
	ks.mu.Unlock()
	if ks.Verify(token) {
		t.Fatal("a never-expiring token must be rejected once the operator opts into TTL")
	}
}

func TestKeyStoreRotateGrace(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	oldToken, err := ks.IssueToken()
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	old := ks.CurrentKey()
	forceRotate(ks)
	newKey, validUntil, coalesced, err := ks.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if coalesced {
		t.Fatal("rotate after backdating current secret must not coalesce")
	}
	if newKey == old {
		t.Fatal("rotate returned the same secret")
	}
	if ks.CurrentKey() != newKey {
		t.Fatal("current secret not updated")
	}
	if !ks.Verify(oldToken) {
		t.Fatal("token minted before rotation must stay valid inside grace")
	}
	newToken, err := ks.IssueToken()
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if !ks.Verify(newToken) {
		t.Fatal("token minted from the new secret must verify")
	}
	if until := time.Until(validUntil); until < 59*time.Minute || until > 61*time.Minute {
		t.Fatalf("previous_valid_until %v not ~1h out", until)
	}
}

func TestKeyStoreGraceExpiry(t *testing.T) {
	ks, path := newTestKeyStore(t, "", time.Hour, 0)
	oldToken, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	forceRotate(ks)
	if _, _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	var ring keyringFile
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &ring); err != nil {
		t.Fatal(err)
	}
	ring.Previous[0].RetiredAt = time.Now().Add(-2 * time.Hour)
	out, _ := json.Marshal(ring)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	ks2, err := LoadOrCreate(path, "", time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ks2.Verify(oldToken) {
		t.Fatal("token from a secret beyond its grace window must not verify")
	}
}

// TestGraceTimesTTL: a token from a retired-but-in-grace secret honours its
// own expiry; a token whose secret is past grace fails even if its own
// expiry is in the future.
func TestGraceTimesTTL(t *testing.T) {
	ks, path := newTestKeyStore(t, "", time.Hour, time.Minute)
	longLivedToken, err := signToken(ks.CurrentKey(), 24*time.Hour) // secret's own token, longer TTL than the store default, still bounded by secret grace
	if err != nil {
		t.Fatal(err)
	}
	forceRotate(ks)
	if _, _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	if !ks.Verify(longLivedToken) {
		t.Fatal("in-grace secret's token with a future expiry must verify")
	}

	// Now push that secret's retirement past grace.
	var ring keyringFile
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &ring); err != nil {
		t.Fatal(err)
	}
	ring.Previous[0].RetiredAt = time.Now().Add(-2 * time.Hour)
	out, _ := json.Marshal(ring)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	ks2, err := LoadOrCreate(path, "", time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ks2.Verify(longLivedToken) {
		t.Fatal("a token whose secret is past grace must fail even with a future embedded expiry")
	}
}

// forceRotate backdates the current secret's CreatedAt so the next Rotate()
// call is not coalesced.
func forceRotate(ks *KeyStore) {
	ks.mu.Lock()
	ks.ring.Current.CreatedAt = ks.ring.Current.CreatedAt.Add(-2 * rotateCoalesce)
	ks.mu.Unlock()
}

func TestRotateCoalescesWithin24h(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	secret := ks.CurrentKey()
	_, _, coalesced1, err := ks.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if !coalesced1 {
		t.Fatal("rotate immediately after mint must coalesce")
	}
	if ks.CurrentKey() != secret {
		t.Fatal("coalesced rotate must not change the current secret")
	}
	// A fresh, valid token is still mintable from the unchanged secret.
	token, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	if !ks.Verify(token) {
		t.Fatal("token minted after a coalesced rotate must verify")
	}
}

// TestRotateEvictionRegression is the maxPreviousKeys truncation bug: 15
// forced rotations (clock injected to defeat coalescing) must not evict a
// still-in-grace secret early.
func TestRotateEvictionRegression(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", 30*24*time.Hour, 0)
	firstSecret := ks.CurrentKey()
	firstToken, err := ks.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	ks.now = func() time.Time { return base }
	for i := 0; i < 15; i++ {
		base = base.Add(25 * time.Hour) // > rotateCoalesce each step
		ks.now = func() time.Time { return base }
		if _, _, coalesced, err := ks.Rotate(); err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		} else if coalesced {
			t.Fatalf("rotate %d unexpectedly coalesced", i)
		}
	}
	_ = firstSecret
	if !ks.Verify(firstToken) {
		t.Fatal("a token from rotation #1's secret must still verify inside the 30-day grace window despite 15 rotations")
	}
}

func TestKeyStoreRotatePrunesAndCaps(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	base := time.Now()
	for i := 0; i < maxPreviousKeys+5; i++ {
		base = base.Add(25 * time.Hour)
		ks.now = func() time.Time { return base }
		if _, _, _, err := ks.Rotate(); err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
	}
	ks.mu.Lock()
	n := len(ks.ring.Previous)
	ks.mu.Unlock()
	if n > maxPreviousKeys {
		t.Fatalf("previous keys = %d, want <= %d", n, maxPreviousKeys)
	}
}

func TestKeyStoreFingerprintNeverFullKey(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour, 0)
	fp := ks.Fingerprint()
	if strings.Contains(fp, strings.TrimPrefix(ks.CurrentKey(), "dk_")) {
		t.Fatalf("fingerprint %q leaks the full key", fp)
	}
	if !strings.HasPrefix(fp, "dk_") {
		t.Fatalf("fingerprint %q missing prefix", fp)
	}
}

func TestKeyStoreFromEnv_TTLUnsetIsZero(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DICTION_KEY_PATH", filepath.Join(dir, "gateway-keys.json"))
	t.Setenv("DICTION_TOKEN_TTL", "")
	ks, err := KeyStoreFromEnv(ModeOptional)
	if err != nil {
		t.Fatal(err)
	}
	ks.mu.Lock()
	ttl := ks.ttl
	ks.mu.Unlock()
	if ttl != 0 {
		t.Fatalf("ttl = %v, want 0 (unset)", ttl)
	}
}

func TestKeyStoreFromEnv_TTLParsed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DICTION_KEY_PATH", filepath.Join(dir, "gateway-keys.json"))
	t.Setenv("DICTION_TOKEN_TTL", "2m")
	ks, err := KeyStoreFromEnv(ModeRequired)
	if err != nil {
		t.Fatal(err)
	}
	ks.mu.Lock()
	ttl := ks.ttl
	ks.mu.Unlock()
	if ttl != 2*time.Minute {
		t.Fatalf("ttl = %v, want 2m", ttl)
	}
}

func TestKeyStoreFromEnv_MalformedTTLIsHardError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DICTION_KEY_PATH", filepath.Join(dir, "gateway-keys.json"))
	t.Setenv("DICTION_TOKEN_TTL", "not-a-duration")
	if _, err := KeyStoreFromEnv(ModeOptional); err == nil {
		t.Fatal("malformed DICTION_TOKEN_TTL must be a hard startup error, not a silent fallback")
	}
}

func TestKeyStoreFromEnv_Off(t *testing.T) {
	ks, err := KeyStoreFromEnv(ModeOff)
	if err != nil || ks != nil {
		t.Fatalf("KeyStoreFromEnv(off) = (%v, %v), want (nil, nil)", ks, err)
	}
}
