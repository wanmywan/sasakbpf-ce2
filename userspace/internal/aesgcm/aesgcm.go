package aesgcm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Seal encrypts plaintext with AES-256-GCM using a random 12-byte nonce.
// Returns nonce || ciphertext+tag bundled (caller base64-encodes).
func Seal(keyHex string, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(keyHex)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ct...), nil
}

// Open decrypts a nonce||ciphertext+tag bundle.
func Open(keyHex string, bundle []byte) ([]byte, error) {
	gcm, err := newGCM(keyHex)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(bundle) < ns+1 {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := bundle[:ns], bundle[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

func DecodeHex(keyHex string) ([]byte, error) {
	k, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("hex decode key: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (256-bit), got %d", len(k))
	}
	return k, nil
}

func newGCM(keyHex string) (cipher.AEAD, error) {
	k, err := DecodeHex(keyHex)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return gcm, nil
}