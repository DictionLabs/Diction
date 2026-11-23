package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func EncryptTranscript(transcript string, clientPubB64 string) (ctB64, serverPubB64 string, err error) {
	clientPubBytes, err := base64.RawURLEncoding.DecodeString(clientPubB64)
	if err != nil {
		return "", "", fmt.Errorf("decode client pubkey: %w", err)
	}
	clientPub, err := ecdh.X25519().NewPublicKey(clientPubBytes)
	if err != nil {
		return "", "", fmt.Errorf("parse client pubkey: %w", err)
	}

	serverPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate server key: %w", err)
	}

	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return "", "", fmt.Errorf("ecdh: %w", err)
	}

	key := hkdfSHA256(shared, []byte("diction-transcript-v1"))

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", "", fmt.Errorf("nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(transcript), nil)

	return base64.RawURLEncoding.EncodeToString(ciphertext),
		base64.RawURLEncoding.EncodeToString(serverPriv.PublicKey().Bytes()),
		nil
}

func LoadStaticKey(b64 string) (*ecdh.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode static key: %w", err)
	}
	return ecdh.X25519().NewPrivateKey(raw)
}

func DecryptRequest(ctB64, clientPubB64 string, serverStaticKey *ecdh.PrivateKey, info string) ([]byte, error) {
	clientPubBytes, err := base64.RawURLEncoding.DecodeString(clientPubB64)
	if err != nil {
		return nil, fmt.Errorf("decode client pubkey: %w", err)
	}
	clientPub, err := ecdh.X25519().NewPublicKey(clientPubBytes)
	if err != nil {
		return nil, fmt.Errorf("parse client pubkey: %w", err)
	}

	shared, err := serverStaticKey.ECDH(clientPub)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	key := hkdfSHA256(shared, []byte(info))

	ctData, err := base64.RawURLEncoding.DecodeString(ctB64)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	if len(ctData) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ctData[:gcm.NonceSize()]
	ciphertext := ctData[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

const hkdfKeyLen = 32

func hkdfSHA256(ikm, info []byte) []byte {
	salt := make([]byte, sha256.Size)
	extractor := hmac.New(sha256.New, salt)
	extractor.Write(ikm)
	prk := extractor.Sum(nil)

	var okm []byte
	var prev []byte
	for i := 1; len(okm) < hkdfKeyLen; i++ {
		h := hmac.New(sha256.New, prk)
		h.Write(prev)
		h.Write(info)
		h.Write([]byte{byte(i)})
		prev = h.Sum(nil)
		okm = append(okm, prev...)
	}
	return okm[:hkdfKeyLen]
}
