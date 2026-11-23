package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

// Tokens: `dk_` + base64url(nonce16 || expiry8 BE || HMAC-SHA256 tag), dotless; expiry 0 = never.
const tokenPrefix = "dk_"

const (
	nonceLen  = 16
	expiryLen = 8
	tagLen    = sha256.Size
	// tokenBlobLen is the decoded length of a token: nonce || expiry || HMAC tag.
	tokenBlobLen = nonceLen + expiryLen + tagLen
)

// verifyVerdict says why a token failed, for logging only; the wire response is always 401.
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

// signToken mints a device token: random nonce, expiry (0 = never) and HMAC tag under secret.
func signToken(secret string, ttl time.Duration) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("mint pairing token: %w", err)
	}
	var expiry uint64
	if ttl > 0 {
		if exp := time.Now().Add(ttl).Unix(); exp > 0 {
			expiry = uint64(exp)
		}
	}
	expBytes := make([]byte, expiryLen)
	binary.BigEndian.PutUint64(expBytes, expiry)
	msg := make([]byte, 0, nonceLen+expiryLen)
	msg = append(msg, nonce...)
	msg = append(msg, expBytes...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	blob := mac.Sum(msg)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(blob), nil
}

// decodeTokenBlob strips the prefix and base64-decodes a token, checking only its length.
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

// tokenExpiry reads a token's expiry without checking its HMAC; ok=false if malformed.
func tokenExpiry(token string) (exp time.Time, ok bool) {
	raw, err := decodeTokenBlob(token)
	if err != nil {
		return time.Time{}, false
	}
	e := binary.BigEndian.Uint64(raw[nonceLen : nonceLen+expiryLen])
	if e == 0 {
		return time.Time{}, true
	}
	return time.Unix(int64(min(e, math.MaxInt64)), 0), true
}

// tokenNoncePrefix returns the first 8 hex chars of a token's nonce for logs; empty if malformed.
func tokenNoncePrefix(token string) string {
	raw, err := decodeTokenBlob(token)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", raw[:4])
}

// verifyToken checks the HMAC over nonce+expiry, then expiry (0 = never, valid only if ttl == 0).
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
