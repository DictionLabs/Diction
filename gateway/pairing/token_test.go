package pairing

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip_NoTTL(t *testing.T) {
	token, err := signToken("secret", 0)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	if !strings.HasPrefix(token, tokenPrefix) {
		t.Fatalf("token %q missing prefix", token)
	}
	if verifyToken(token, "secret", 0, time.Now()) != verdictOK {
		t.Fatal("token minted with no TTL must verify indefinitely")
	}
	// Even far in the future, a never-expiring token still verifies while TTL stays unset.
	if verifyToken(token, "secret", 0, time.Now().Add(100*365*24*time.Hour)) != verdictOK {
		t.Fatal("expiry==0 token must never expire while TTL is unset")
	}
}

func TestSignVerifyRoundTrip_WithTTL(t *testing.T) {
	token, err := signToken("secret", time.Hour)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	now := time.Now()
	if verifyToken(token, "secret", time.Hour, now) != verdictOK {
		t.Fatal("freshly minted token must verify")
	}
	if verifyToken(token, "secret", time.Hour, now.Add(59*time.Minute)) != verdictOK {
		t.Fatal("token one second before expiry must verify")
	}
	if verifyToken(token, "secret", time.Hour, now.Add(61*time.Minute)) != verdictExpired {
		t.Fatal("token past expiry must report expired")
	}
}

func TestVerifyToken_TamperedExpiryFailsHMAC(t *testing.T) {
	token, err := signToken("secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decodeTokenBlob(token)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in the expiry field (bytes 16..24).
	raw[20] ^= 0xFF
	tampered := tokenPrefix + rawURLEncode(raw)
	if verifyToken(tampered, "secret", time.Hour, time.Now()) != verdictInvalid {
		t.Fatal("tampering with the signed expiry must fail the HMAC tag, proving expiry is signed")
	}
}

func TestVerifyToken_WrongSecret(t *testing.T) {
	token, err := signToken("secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if verifyToken(token, "other-secret", 0, time.Now()) != verdictInvalid {
		t.Fatal("token signed under a different secret must not verify")
	}
}

func TestVerifyToken_Garbage(t *testing.T) {
	for _, bad := range []string{"", "dk_", "dk_not-base64!!!", "dk_" + rawURLEncode([]byte("too short"))} {
		if verifyToken(bad, "secret", 0, time.Now()) != verdictInvalid {
			t.Fatalf("garbage token %q must be invalid", bad)
		}
	}
}

// TestExpiryPolicyMatrix is the "no silent exemptions" guarantee: enabling
// TTL rejects both never-expiring and (tested separately in keystore_test.go)
// legacy credentials.
func TestExpiryPolicyMatrix(t *testing.T) {
	now := time.Now()
	neverExpiring, err := signToken("secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	inDate, err := signToken("secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		token string
		ttl   time.Duration
		want  verifyVerdict
	}{
		{"ttl-unset/never-expiring accepted", neverExpiring, 0, verdictOK},
		{"ttl-set/never-expiring rejected", neverExpiring, time.Hour, verdictExpired},
		{"ttl-set/in-date accepted", inDate, time.Hour, verdictOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyToken(tc.token, "secret", tc.ttl, now); got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTokenExpiry(t *testing.T) {
	neverExpiring, err := signToken("secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if exp, ok := tokenExpiry(neverExpiring); !ok || !exp.IsZero() {
		t.Fatalf("never-expiring token: exp=%v ok=%v, want zero time and ok", exp, ok)
	}

	expiring, err := signToken("secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	exp, ok := tokenExpiry(expiring)
	if !ok || exp.IsZero() {
		t.Fatalf("expiring token: exp=%v ok=%v, want a real time", exp, ok)
	}
	if until := time.Until(exp); until < 59*time.Minute || until > 61*time.Minute {
		t.Fatalf("expiry %v not ~1h out", until)
	}

	for _, bad := range []string{"", "dk_", "garbage", "dk_" + rawURLEncode([]byte("short"))} {
		if _, ok := tokenExpiry(bad); ok {
			t.Fatalf("garbage token %q must report not-ok, never a bogus date", bad)
		}
	}
}

func TestTokenNoncePrefixNeverLeaksSecretOrFullToken(t *testing.T) {
	token, err := signToken("very-secret-value", 0)
	if err != nil {
		t.Fatal(err)
	}
	prefix := tokenNoncePrefix(token)
	if len(prefix) != 8 {
		t.Fatalf("nonce prefix %q wrong length", prefix)
	}
	raw, err := decodeTokenBlob(token)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", raw[:4]); prefix != want {
		t.Fatalf("prefix %q should be derived from the token's own nonce, want %q", prefix, want)
	}
	if strings.Contains(prefix, "very-secret-value") {
		t.Fatal("nonce prefix must never leak the secret")
	}
	if tokenNoncePrefix("garbage") != "" {
		t.Fatal("malformed token must report empty nonce prefix")
	}
}

func rawURLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
