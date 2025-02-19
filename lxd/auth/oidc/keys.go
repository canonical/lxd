package oidc

import (
	"context"
	"crypto/sha512"
	"fmt"
	"io"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"

	"github.com/canonical/lxd/lxd/db/cluster/secret"
)

// extractKeys derives a psuedorandom key from the given cluster-wide secret and salt. If no salt is given, the cluster-wide salt is used.
func newPseudoRandomKey(s secret.Secret, salt []byte) ([]byte, error) {
	key, clusterSalt, err := s.KeyAndSalt()
	if err != nil {
		return nil, err
	}

	if salt == nil {
		salt = clusterSalt
	}

	// Extract a pseudo-random key from the cluster secret.
	return hkdf.Extract(sha512.New, key, salt), nil
}

// newSecureReader calls HKDF EXPAND on the pseudo random key to derive a deterministic, but secure key based on the secret
// and salt.
func newSecureReader(pseudoRandomKey []byte, label string) io.Reader {
	return hkdf.Expand(sha512.New, pseudoRandomKey, []byte(label))
}

// Definitions for cookie hash and block keys.
func cookieEncryptionKeys() (hash *key, block *key) {
	hash = &key{
		label: "INTEGRITY",
		size:  64,
	}

	block = &key{
		label: "ENCRYPTION",
		size:  32,
	}

	return hash, block
}

// Definition for session token signature keys.
func sessionSignature() *key {
	return &key{
		label: "SIGNATURE",
		size:  256,
	}
}

// key defines an encryption key. The value field is populated by extractKeys.
type key struct {
	label string
	size  int
	value []byte
}

// extractKeys derives a hash and block key from the given secret and salt. If no salt is given, the cluster-wide salt is used.
func extractKeys(s secret.Secret, salt []byte, keys ...*key) error {
	prk, err := newPseudoRandomKey(s, salt)
	if err != nil {
		return err
	}

	for _, key := range keys {
		key.value = make([]byte, key.size)
		n, err := io.ReadFull(newSecureReader(prk, key.label), key.value)
		if err != nil || n != key.size {
			return fmt.Errorf("Failed creating %q key: %w", key.label, err)
		}
	}

	return nil
}

func sessionKey(ctx context.Context, clusterSecretFunc func(context.Context) (secret.Secret, bool, error), sessionID uuid.UUID) ([]byte, error) {
	s, _, err := clusterSecretFunc(ctx)
	if err != nil {
		return nil, err
	}

	sessionKey := sessionSignature()
	err = extractKeys(s, sessionID[:], sessionKey)
	if err != nil {
		return nil, err
	}

	return sessionKey.value, nil
}

func hashAndBlockKeys(s secret.Secret) (hash []byte, block []byte, err error) {
	hashKey, blockKey := cookieEncryptionKeys()
	err = extractKeys(s, nil, []*key{hashKey, blockKey}...)
	if err != nil {
		return nil, nil, err
	}

	return hashKey.value, blockKey.value, nil
}
