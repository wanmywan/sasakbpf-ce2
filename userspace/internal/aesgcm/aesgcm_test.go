package aesgcm

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// Round-trip test with known key.
func TestSealOpenRoundTrip(t *testing.T) {
	key := strings.Repeat("00", 32) // 32-byte zero key
	pt := []byte("hello sasakbpf")
	bundle, err := Seal(key, pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	out, err := Open(key, bundle)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(out) != string(pt) {
		t.Fatalf("got %q want %q", out, pt)
	}
}

// Multiple seals with same plaintext must produce different ciphertexts
// (random nonce). Tamper detection must fail.
func TestNonceRandomnessAndTamper(t *testing.T) {
	key := strings.Repeat("11", 32)
	pt := []byte("round two")
	b1, _ := Seal(key, pt)
	b2, _ := Seal(key, pt)
	if string(b1) == string(b2) {
		t.Fatalf("seals identical — nonce not random")
	}
	b1[len(b1)-1] ^= 0x01
	if _, err := Open(key, b1); err == nil {
		t.Fatalf("Open succeeded on tampered ciphertext — GCM broken")
	}
}

// Wrong-key verification must fail.
func TestWrongKey(t *testing.T) {
	bundle, _ := Seal(strings.Repeat("22", 32), []byte("data"))
	if _, err := Open(strings.Repeat("33", 32), bundle); err == nil {
		t.Fatalf("Open succeeded with wrong key")
	}
}

// Base64 transport shape used by protocol.
func TestBase64TransportRoundTrip(t *testing.T) {
	key := hex.EncodeToString([]byte("0123456789ABCDEF0123456789ABCDEF")) // 32 bytes
	pt := []byte("transport me")
	bundle, _ := Seal(key, pt)
	b64 := base64.StdEncoding.EncodeToString(bundle)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("b64 decode: %v", err)
	}
	out, err := Open(key, raw)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(out) != string(pt) {
		t.Fatalf("got %q want %q", out, pt)
	}
}