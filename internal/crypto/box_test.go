package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSealOpenRoundTripAndTamperDetection(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(key[:31]); err == nil {
		t.Fatal("short key accepted")
	}
	sealed, err := box.Seal([]byte("secret-value"), []byte("account-1"))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := box.Seal([]byte("secret-value"), []byte("account-1"))
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce was reused: identical ciphertexts")
	}
	plain, err := box.Open(sealed, []byte("account-1"))
	if err != nil || string(plain) != "secret-value" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
	if _, err = box.Open(sealed, []byte("account-2")); err == nil {
		t.Fatal("ciphertext bound to another record was accepted")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = box.Open(sealed, []byte("account-1")); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	if _, err = box.Open([]byte("short"), nil); err == nil {
		t.Fatal("truncated ciphertext was accepted")
	}
}

func TestKeyringOpensLegacyAndRotates(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	legacyBox, _ := New(oldKey)
	raw, _ := legacyBox.Seal([]byte("token"), []byte("conn-1"))
	legacy := base64Std(raw)

	ring, err := NewKeyring(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.Open(legacy, []byte("conn-1"))
	if err != nil || string(plain) != "token" {
		t.Fatalf("legacy ciphertext not readable: %q %v", plain, err)
	}
	if !ring.NeedsRotation(legacy) {
		t.Fatal("legacy value must need rotation")
	}
	rotated, err := ring.Reseal(legacy, []byte("conn-1"))
	if err != nil {
		t.Fatal(err)
	}
	if ring.NeedsRotation(rotated) || !bytes.HasPrefix([]byte(rotated), []byte("k1:"+ring.ActiveID()+":")) {
		t.Fatalf("rotated value has wrong format: %s", rotated)
	}
	if plain, err = ring.Open(rotated, []byte("conn-1")); err != nil || string(plain) != "token" {
		t.Fatalf("rotated ciphertext not readable: %v", err)
	}
	onlyNew, _ := NewKeyring(newKey)
	if _, err = onlyNew.Open(legacy, []byte("conn-1")); err == nil {
		t.Fatal("legacy value decrypted without its key")
	}
	if _, err = onlyNew.Open("k1:deadbeef:AAAA", []byte("conn-1")); err == nil {
		t.Fatal("unknown key id was accepted")
	}
	if _, err = ring.Open(rotated, []byte("conn-2")); err == nil {
		t.Fatal("aad mismatch was accepted")
	}
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
