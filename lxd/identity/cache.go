package identity

import (
	"crypto/x509"
	"errors"
	"net/http"
	"sync"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Cache represents a thread-safe in-memory cache of certificate identities in the database.
type Cache struct {
	// entries is a map of certificate fingerprint to CacheEntry.
	entries map[string]*CacheEntry

	mu sync.RWMutex
}

// CacheEntry represents an identity.
type CacheEntry struct {
	Identifier string
	Type       string

	// Certificate is pre-computed for identities with AuthenticationMethod api.AuthenticationMethodTLS.
	Certificate *x509.Certificate
}

// Get returns a single CacheEntry by its authentication method and identifier.
func (c *Cache) Get(fingerprint string) (*CacheEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.entries == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q not found", fingerprint)
	}

	entry, ok := c.entries[fingerprint]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q not found", fingerprint)
	}

	if entry == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q not found", fingerprint)
	}

	entryCopy := *entry
	return &entryCopy, nil
}

// GetByType returns a map of identifier to CacheEntry, where all entries have the given identity type.
func (c *Cache) GetByType(identityType string) map[string]CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entriesOfType := make(map[string]CacheEntry)
	for _, entry := range c.entries {
		if entry != nil && entry.Type == identityType {
			entriesOfType[entry.Identifier] = *entry
		}
	}

	return entriesOfType
}

// Clone returns a copy of the cache entries.
func (c *Cache) Clone() map[string]CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// A go map is a pointer. To make the cache thread-safe we need to copy entriesOfAuthMethod into a new map.
	entriesCopy := make(map[string]CacheEntry, len(c.entries))
	for identifier, entry := range c.entries {
		if entry != nil {
			entriesCopy[identifier] = *entry
		}
	}

	return entriesCopy
}

// ReplaceAll deletes all entries and identity provider groups from the cache and replaces them with the given values.
func (c *Cache) ReplaceAll(entries []CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
	for _, entry := range entries {
		if entry.Certificate == nil {
			return errors.New("Cache entries must have a certificate")
		}

		e := entry
		c.entries[entry.Identifier] = &e
	}

	return nil
}

// X509Certificates returns a map of certificate fingerprint to the x509 certificates of TLS identities. Identity types
// can be passed in to filter the results. If no identity types are given, all certificates are returned.
func (c *Cache) X509Certificates(identityTypes ...string) map[string]x509.Certificate {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.entries == nil {
		return nil
	}

	certificates := make(map[string]x509.Certificate, len(c.entries))
	for _, tlsEntry := range c.entries {
		if (len(identityTypes) == 0 || shared.ValueInSlice(tlsEntry.Type, identityTypes)) && tlsEntry.Certificate != nil {
			certificates[tlsEntry.Identifier] = *tlsEntry.Certificate
		}
	}

	return certificates
}
