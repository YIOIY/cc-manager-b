package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	return &Box{a}, nil
}
func (b *Box) Seal(plain, aad []byte) ([]byte, error) {
	n := make([]byte, b.aead.NonceSize())
	if _, e := io.ReadFull(rand.Reader, n); e != nil {
		return nil, e
	}
	return b.aead.Seal(n, n, plain, aad), nil
}
func (b *Box) Open(data, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(data) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, data[:ns], data[ns:], aad)
}
