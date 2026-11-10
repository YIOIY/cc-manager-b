package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Stored ciphertext format:
//
//	k1:<key-id>:<base64(nonce||ciphertext)>
//
// Legacy rows hold bare base64 without a prefix; they are decrypted by trying
// the active key first and then every previous key, and are rewritten in the
// new format by `cc-manager rotate-key`.
const formatPrefix = "k1:"

// KeyID derives a short, non-secret identifier for a key.
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

// Keyring seals with the active key and opens with any configured key.
type Keyring struct {
	activeID string
	active   *Box
	boxes    map[string]*Box
	order    []string
}

// NewKeyring builds a keyring from the active key and optional previous keys.
func NewKeyring(active []byte, previous ...[]byte) (*Keyring, error) {
	box, err := New(active)
	if err != nil {
		return nil, fmt.Errorf("active key: %w", err)
	}
	k := &Keyring{activeID: KeyID(active), active: box, boxes: map[string]*Box{}}
	k.boxes[k.activeID] = box
	k.order = append(k.order, k.activeID)
	for i, key := range previous {
		id := KeyID(key)
		if _, exists := k.boxes[id]; exists {
			continue
		}
		b, err := New(key)
		if err != nil {
			return nil, fmt.Errorf("previous key %d: %w", i+1, err)
		}
		k.boxes[id] = b
		k.order = append(k.order, id)
	}
	return k, nil
}

func (k *Keyring) ActiveID() string { return k.activeID }

// Seal encrypts plain bound to aad and returns the storable string.
func (k *Keyring) Seal(plain, aad []byte) (string, error) {
	sealed, err := k.active.Seal(plain, aad)
	if err != nil {
		return "", err
	}
	return formatPrefix + k.activeID + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a stored string produced by Seal or by the legacy encoder.
func (k *Keyring) Open(encoded string, aad []byte) ([]byte, error) {
	if id, body, ok := split(encoded); ok {
		box := k.boxes[id]
		if box == nil {
			return nil, fmt.Errorf("encryption key %s is not configured", id)
		}
		raw, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil, err
		}
		return box.Open(raw, aad)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, id := range k.order {
		plain, err := k.boxes[id].Open(raw, aad)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no key available")
	}
	return nil, lastErr
}

// NeedsRotation reports whether a stored value is legacy-encoded or was
// sealed with a key other than the active one.
func (k *Keyring) NeedsRotation(encoded string) bool {
	id, _, ok := split(encoded)
	return !ok || id != k.activeID
}

// Reseal decrypts with whichever key applies and re-encrypts with the active key.
func (k *Keyring) Reseal(encoded string, aad []byte) (string, error) {
	plain, err := k.Open(encoded, aad)
	if err != nil {
		return "", err
	}
	return k.Seal(plain, aad)
}

func split(encoded string) (id, body string, ok bool) {
	if !strings.HasPrefix(encoded, formatPrefix) {
		return "", "", false
	}
	rest := encoded[len(formatPrefix):]
	id, body, found := strings.Cut(rest, ":")
	if !found || id == "" || body == "" {
		return "", "", false
	}
	return id, body, true
}
