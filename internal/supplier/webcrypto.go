package supplier

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
)

// EncryptLoginPassword produces the console's "hybrid-aesgcm" envelope: the
// password is encrypted with a fresh AES-256-GCM key, and that key is wrapped
// with the server's RSA public key (OAEP, SHA-256 for both the hash and
// MGF1). The envelope is a base64-encoded UTF-8 JSON document, which is what
// the console's login form sends as enc_password.
func EncryptLoginPassword(publicKeyPEM, password string) (string, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return "", errors.New("supplier public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", errors.New("supplier public key is not a PKIX key")
	}
	pub, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("supplier public key is not RSA")
	}
	key := make([]byte, 32)
	iv := make([]byte, 12)
	if _, err = rand.Read(key); err != nil {
		return "", err
	}
	if _, err = rand.Read(iv); err != nil {
		return "", err
	}
	aead, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(aead)
	if err != nil {
		return "", err
	}
	// Seal appends the 16-byte tag, matching forge's ciphertext ‖ tag layout.
	sealed := gcm.Seal(nil, iv, []byte(password), nil)
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, key, nil)
	if err != nil {
		return "", err
	}
	envelope, err := json.Marshal(map[string]string{
		"__enc__": "hybrid-aesgcm",
		"k":       base64.StdEncoding.EncodeToString(wrapped),
		"iv":      base64.StdEncoding.EncodeToString(iv),
		"ct":      base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(envelope), nil
}
