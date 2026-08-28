package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestKeyStore(t *testing.T, envKey string, grace time.Duration) (*KeyStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway-keys.json")
	ks, err := LoadOrCreateKeyStore(path, envKey, grace)
	if err != nil {
		t.Fatalf("LoadOrCreateKeyStore: %v", err)
	}
	return ks, path
}

func TestKeyStoreGeneratesAndPersists(t *testing.T) {
	ks, path := newTestKeyStore(t, "", time.Hour)
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
	// Reload from disk: same key.
	ks2, err := LoadOrCreateKeyStore(path, "", time.Hour)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ks2.CurrentKey() != key {
		t.Fatalf("reload produced a different key")
	}
	// File permissions are owner-only.
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
	ks, err := LoadOrCreateKeyStore(path, "", time.Hour)
	if err != nil {
		t.Fatalf("LoadOrCreateKeyStore on corrupt file: %v", err)
	}
	if !strings.HasPrefix(ks.CurrentKey(), "dk_") {
		t.Fatalf("no key regenerated after corrupt file")
	}
}

func TestKeyStoreEnvPinned(t *testing.T) {
	ks, path := newTestKeyStore(t, "dk_pinned_key_value", time.Hour)
	if !ks.Pinned() {
		t.Fatal("expected pinned store")
	}
	if ks.CurrentKey() != "dk_pinned_key_value" {
		t.Fatalf("current = %q, want env key", ks.CurrentKey())
	}
	if _, _, err := ks.Rotate(); err != ErrKeyPinned {
		t.Fatalf("Rotate on pinned = %v, want ErrKeyPinned", err)
	}
	// Pinned keys are never written to disk.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pinned key was persisted to disk")
	}
}

func TestKeyStoreVerify(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	key := ks.CurrentKey()
	if !ks.Verify(key) {
		t.Fatal("current key must verify")
	}
	for _, bad := range []string{"", "dk_wrong", key + "x", key[:len(key)-1]} {
		if ks.Verify(bad) {
			t.Fatalf("token %q must not verify", bad)
		}
	}
}

func TestKeyStoreRotateGrace(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	old := ks.CurrentKey()
	newKey, validUntil, err := ks.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newKey == old {
		t.Fatal("rotate returned the same key")
	}
	if ks.CurrentKey() != newKey {
		t.Fatal("current key not updated")
	}
	if !ks.Verify(old) {
		t.Fatal("retired key must stay valid inside grace")
	}
	if !ks.Verify(newKey) {
		t.Fatal("new key must verify")
	}
	if until := time.Until(validUntil); until < 59*time.Minute || until > 61*time.Minute {
		t.Fatalf("previous_valid_until %v not ~1h out", until)
	}
}

func TestKeyStoreGraceExpiry(t *testing.T) {
	ks, path := newTestKeyStore(t, "", time.Hour)
	old := ks.CurrentKey()
	if _, _, err := ks.Rotate(); err != nil {
		t.Fatal(err)
	}
	// Backdate the retirement past the grace window, on disk, then reload.
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
	ks2, err := LoadOrCreateKeyStore(path, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ks2.Verify(old) {
		t.Fatal("key beyond grace must not verify")
	}
}

func TestKeyStoreRotatePrunesAndCaps(t *testing.T) {
	ks, _ := newTestKeyStore(t, "", time.Hour)
	for i := 0; i < maxPreviousKeys+5; i++ {
		if _, _, err := ks.Rotate(); err != nil {
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
	ks, _ := newTestKeyStore(t, "", time.Hour)
	fp := ks.Fingerprint()
	if strings.Contains(fp, strings.TrimPrefix(ks.CurrentKey(), "dk_")) {
		t.Fatalf("fingerprint %q leaks the full key", fp)
	}
	if !strings.HasPrefix(fp, "dk_") {
		t.Fatalf("fingerprint %q missing prefix", fp)
	}
}
