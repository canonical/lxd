package encryption

import (
	"crypto/hmac"
	"crypto/sha512"
	"fmt"
)

var hashFunc = sha512.New

// CookieHashKey returns a key suitable for cookie integrity checks with HMAC-SHA512.
func CookieHashKey(secret []byte, salt []byte) ([]byte, error) {
	return deriveKey(secret, salt, "INTEGRITY", 64)
}

// CookieBlockKey returns a key suitable for cookie encryption with AES-256.
func CookieBlockKey(secret []byte, salt []byte) ([]byte, error) {
	return deriveKey(secret, salt, "ENCRYPTION", 32)
}

// TokenSigningKey returns a key suitable for signing JWTs with HMAC-SHA512.
func TokenSigningKey(secret []byte, salt []byte) ([]byte, error) {
	return deriveKey(secret, salt, "SIGNATURE", 64)
}

// deriveKey uses HMAC to derive a key from a secret, a salt, and a separator. We can use HMAC directly because our
// initial key material is uniformly random and of sufficient length.
func deriveKey(secret []byte, salt []byte, usageSeparator string, length uint) ([]byte, error) {
	maxSize := hashFunc().Size()
	if int(length) > maxSize {
		return nil, fmt.Errorf("Cannot derive keys larger than %d", maxSize)
	}

	// Extract a pseudo-random key from the secret value.
	h := hmac.New(hashFunc, secret)

	// Write salt.
	_, err := h.Write(salt)
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure key: %w", err)
	}

	// Write separator.
	_, err = h.Write([]byte(usageSeparator))
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure key: %w", err)
	}

	// Get the key.
	key := h.Sum(nil)[:int(length)]
	return key, nil
}
