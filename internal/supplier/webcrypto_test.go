package supplier

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
)

func publicKeyPEM(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// decryptLoginPassword is the server side of the console's hybrid scheme.
func decryptLoginPassword(t *testing.T, priv *rsa.PrivateKey, encoded string) (string, map[string]string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("outer base64: %v", err)
	}
	var env map[string]string
	if err = json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope json: %v", err)
	}
	wrapped, _ := base64.StdEncoding.DecodeString(env["k"])
	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, wrapped, nil)
	if err != nil {
		t.Fatalf("oaep: %v", err)
	}
	iv, _ := base64.StdEncoding.DecodeString(env["iv"])
	ct, _ := base64.StdEncoding.DecodeString(env["ct"])
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	if len(key) != 32 || len(iv) != 12 {
		t.Fatalf("key %d bytes, iv %d bytes", len(key), len(iv))
	}
	return string(plain), env
}

func TestEncryptLoginPasswordRoundTrip(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub := publicKeyPEM(t, &priv.PublicKey)
	first, err := EncryptLoginPassword(pub, "p@ss 密码")
	if err != nil {
		t.Fatal(err)
	}
	plain, env := decryptLoginPassword(t, priv, first)
	if plain != "p@ss 密码" {
		t.Fatalf("decrypted %q", plain)
	}
	if env["__enc__"] != "hybrid-aesgcm" {
		t.Fatalf("envelope marker %q", env["__enc__"])
	}
	second, err := EncryptLoginPassword(pub, "p@ss 密码")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("two encryptions of the same password must differ (random key and iv)")
	}
}

func TestEncryptLoginPasswordRejectsNonRSA(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EncryptLoginPassword(publicKeyPEM(t, &ec.PublicKey), "x"); err == nil {
		t.Fatal("accepted an ECDSA public key")
	}
	if _, err = EncryptLoginPassword("not a pem", "x"); err == nil {
		t.Fatal("accepted garbage")
	}
}
