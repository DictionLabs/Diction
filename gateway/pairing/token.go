package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Pairing tokens are the self-hosted community gateway's own credential —
// entirely separate from the cloud build's Apple-JWS/trial auth. `dk_` +
// base64url(nonce(16B) || expiryUnixSeconds(8B big-endian) || HMAC-SHA256(nonce||expiry, secret)(32B)),
// zero dots (so the cloud auth middleware's dot-count discriminator — 1 dot
// = trial HMAC, 2 dots = Apple JWS — can never confuse one for its own
// tokens). expiry == 0 is the sentinel for "never expires".
//
// The pairing secret signs pairing tokens and nothing else. Any future
// second use of this secret must add domain separation (a distinct prefix
// inside the MAC input) rather than reuse this construction as-is. Any
// future revocation list must key on the decoded nonce bytes, never the
// token string — Go's base64 decoder tolerates non-canonical encodings, so
// one token has multiple string forms.
const tokenPrefix = "dk_"

const (
	nonceLen  = 16
	expiryLen = 8
	tagLen    = sha256.Size
	// tokenBlobLen is the decoded byte length of a well-formed token:
	// nonce || expiry || HMAC tag.
	tokenBlobLen = nonceLen + expiryLen + tagLen
)

// verifyVerdict distinguishes why a token failed, for logging only — the
// wire response is a uniform 401 invalid_key in every case (Expiry policy
// rule 1: no refresh-token semantics, so there is nothing a client can act
// on differently). Support triage ("why did my phone unpair") is the sole
// consumer of the distinction.
type verifyVerdict int

const (
	verdictInvalid verifyVerdict = iota
	verdictOK
	verdictExpired
)

func (v verifyVerdict) String() string {
	switch v {
	case verdictOK:
		return "ok"
	case verdictExpired:
		return "expired"
	default:
		return "invalid"
	}
}

// signToken mints a device bearer token: a fresh random nonce, an expiry
// (zero when ttl <= 0, meaning "never expires"), and an HMAC-SHA256 tag
// over both under secret. Anyone holding secret can verify (or forge) a
// token, but a token alone reveals nothing about secret. The expiry lives
// inside the signed material, so it is tamper-evident — flipping a byte of
// it fails the tag — and at a fixed byte offset, so any client (including
// the iOS app) can read it with base64-decode + an 8-byte read, no parser
// needed.
func signToken(secret string, ttl time.Duration) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("mint pairing token: %w", err)
	}
	var expiry uint64
	if ttl > 0 {
		expiry = uint64(time.Now().Add(ttl).Unix())
	}
	expBytes := make([]byte, expiryLen)
	binary.BigEndian.PutUint64(expBytes, expiry)
	msg := append(nonce, expBytes...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	blob := mac.Sum(msg)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(blob), nil
}

// decodeTokenBlob strips the prefix and base64-decodes a token, checking
// only its length — no HMAC check, no secret needed.
func decodeTokenBlob(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, tokenPrefix))
	if err != nil {
		return nil, err
	}
	if len(raw) != tokenBlobLen {
		return nil, fmt.Errorf("pairing: token wrong length: %d bytes", len(raw))
	}
	return raw, nil
}

// tokenExpiry reads the expiry field out of a token's own bytes, without
// verifying its HMAC — the verifier and the startup/verify log lines are
// its only consumers, so it stays unexported. ok=false means the token
// isn't well-formed (wrong length, bad base64) — never a bogus date. A
// well-formed but never-expiring token (expiry == 0) reports ok=true with
// a zero time.Time.
func tokenExpiry(token string) (exp time.Time, ok bool) {
	raw, err := decodeTokenBlob(token)
	if err != nil {
		return time.Time{}, false
	}
	e := binary.BigEndian.Uint64(raw[nonceLen : nonceLen+expiryLen])
	if e == 0 {
		return time.Time{}, true
	}
	return time.Unix(int64(e), 0), true
}

// tokenNoncePrefix returns the first 8 hex chars of a token's nonce, for
// loggable identity — never the full token, never the secret (same
// precedent as KeyStore.Fingerprint). Empty for a malformed token.
func tokenNoncePrefix(token string) string {
	raw, err := decodeTokenBlob(token)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", raw[:4])
}

// verifyToken recomputes the HMAC tag from the token's own nonce and
// expiry — so a tampered expiry fails the tag — then applies the Expiry
// policy: expiry == 0 passes only when ttl == 0 (a never-expiring token
// survives only while the operator hasn't opted into TTL enforcement); a
// nonzero expiry passes while now is before it. hmac.Equal is
// constant-time.
func verifyToken(token, secret string, ttl time.Duration, now time.Time) verifyVerdict {
	raw, err := decodeTokenBlob(token)
	if err != nil {
		return verdictInvalid
	}
	msg, tag := raw[:nonceLen+expiryLen], raw[nonceLen+expiryLen:]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return verdictInvalid
	}
	exp, _ := tokenExpiry(token) // already known well-formed above
	if exp.IsZero() {
		if ttl == 0 {
			return verdictOK
		}
		return verdictExpired
	}
	if now.Before(exp) {
		return verdictOK
	}
	return verdictExpired
}
