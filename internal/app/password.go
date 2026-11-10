package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow the OWASP minimum recommendation for a single
// interactive login; they are encoded in the hash so they can be raised later.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns a PHC-formatted argon2id string suitable for
// CC_MANAGER_ADMIN_PASSWORD_HASH.
func HashPassword(password string) (string, error) {
	if len(password) < minPasswordLength {
		return "", fmt.Errorf("password must contain at least %d characters", minPasswordLength)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

func parsePasswordHash(encoded string) (argonParams, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return argonParams{}, errors.New("hash must be an argon2id PHC string")
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, errors.New("hash parameters are invalid")
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return argonParams{}, errors.New("hash salt is invalid")
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(p.key) < 16 {
		return argonParams{}, errors.New("hash digest is invalid")
	}
	if p.memory < 8*1024 || p.time == 0 || p.threads == 0 {
		return argonParams{}, errors.New("hash parameters are too weak")
	}
	return p, nil
}

func validatePasswordHash(encoded string) error {
	_, err := parsePasswordHash(encoded)
	return err
}

// verifyPassword accepts either a stored argon2id hash or a plaintext secret.
// Both comparisons are constant time for the digest; the argon2 computation
// itself dominates the cost and is identical for right and wrong passwords.
func verifyPassword(got, plaintext, hash string) bool {
	if hash != "" {
		p, err := parsePasswordHash(hash)
		if err != nil {
			return false
		}
		derived := argon2.IDKey([]byte(got), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
		return subtle.ConstantTimeCompare(derived, p.key) == 1
	}
	if len(plaintext) < minPasswordLength {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(plaintext)) == 1
}
