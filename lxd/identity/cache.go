package identity

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"sync"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Cache represents a thread-safe in-memory cache of the identities in the database.
type Cache struct {
	// entries is a map of authentication method to map of identifier to CacheEntry. The identifier is either a
	// certificate fingerprint (tls) or an email address (oidc).
	entries map[string]map[string]*CacheEntry

	// identityProviderGroups is a map of identity provider group name to slice of LXD group names.
	identityProviderGroups map[string]*[]string
	mu                     sync.RWMutex
}

// CacheEntry represents an identity.
type CacheEntry struct {
	Identifier           string
	Name                 string
	AuthenticationMethod string
	IdentityType         string
	Projects             []string
	Groups               []string

	// Certificate is optional. It is pre-computed for identities with AuthenticationMethod api.AuthenticationMethodTLS.
	Certificate *x509.Certificate

	// Subject is optional. It is only set when AuthenticationMethod is api.AuthenticationMethodOIDC.
	Subject string
}

// Get returns a single CacheEntry by its authentication method and identifier.
func (c *Cache) Get(authenticationMethod string, identifier string) (*CacheEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.entries == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q (%s) not found", identifier, authenticationMethod)
	}

	entriesByAuthMethod, ok := c.entries[authenticationMethod]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q (%s) not found", identifier, authenticationMethod)
	}

	entry, ok := entriesByAuthMethod[identifier]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q (%s) not found", identifier, authenticationMethod)
	}

	if entry == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity %q (%s) not found", identifier, authenticationMethod)
	}

	entryCopy := *entry
	return &entryCopy, nil
}

// GetByType returns a map of identifier to CacheEntry, where all entries have the given identity type.
func (c *Cache) GetByType(identityType string) map[string]CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Explicitly ignore the error here. It is expected that the caller will use the constants defined in shared/api.
	authenticationMethod, _ := AuthenticationMethodFromIdentityType(identityType)
	entriesByAuthMethod, ok := c.entries[authenticationMethod]
	if !ok {
		return nil
	}

	entriesOfType := make(map[string]CacheEntry)
	for _, entry := range entriesByAuthMethod {
		if entry != nil && entry.IdentityType == identityType {
			entriesOfType[entry.Identifier] = *entry
		}
	}

	return entriesOfType
}

// GetByAuthenticationMethod returns a map of identifier to CacheEntry, where all entries have the given authentication
// method.
func (c *Cache) GetByAuthenticationMethod(authenticationMethod string) map[string]CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entriesOfAuthMethod, ok := c.entries[authenticationMethod]
	if !ok {
		return nil
	}

	// A go map is a pointer. To make the cache thread-safe we need to copy entriesOfAuthMethod into a new map.
	entriesOfAuthMethodCopy := make(map[string]CacheEntry, len(entriesOfAuthMethod))
	for identifier, entry := range entriesOfAuthMethod {
		if entry != nil {
			entriesOfAuthMethodCopy[identifier] = *entry
		}
	}

	return entriesOfAuthMethodCopy
}

// ReplaceAll deletes all entries and identity provider groups from the cache and replaces them with the given values.
func (c *Cache) ReplaceAll(entries []CacheEntry, idpGroups map[string][]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]map[string]*CacheEntry)
	for _, entry := range entries {
		if entry.AuthenticationMethod == api.AuthenticationMethodTLS && entry.Certificate == nil {
			return fmt.Errorf("Identity cache entries of type %q must have a certificate", api.AuthenticationMethodTLS)
		}

		_, ok := c.entries[entry.AuthenticationMethod]
		if !ok {
			c.entries[entry.AuthenticationMethod] = make(map[string]*CacheEntry)
		}

		e := entry
		c.entries[entry.AuthenticationMethod][entry.Identifier] = &e
	}

	c.identityProviderGroups = make(map[string]*[]string, len(idpGroups))
	for idpGroupName, authGroupNames := range idpGroups {
		authGroupNamesCopy := make([]string, 0, len(authGroupNames))
		authGroupNamesCopy = append(authGroupNamesCopy, authGroupNames...)
		c.identityProviderGroups[idpGroupName] = &authGroupNamesCopy
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

	tlsEntries, ok := c.entries[api.AuthenticationMethodTLS]
	if !ok {
		return nil
	}

	certificates := make(map[string]x509.Certificate, len(tlsEntries))
	for _, tlsEntry := range tlsEntries {
		if (len(identityTypes) == 0 || shared.ValueInSlice(tlsEntry.IdentityType, identityTypes)) && tlsEntry.Certificate != nil {
			certificates[tlsEntry.Identifier] = *tlsEntry.Certificate
		}
	}

	return certificates
}

// GetByOIDCSubject returns a CacheEntry with the given subject or returns an api.StatusError with http.StatusNotFound.
func (c *Cache) GetByOIDCSubject(subject string) (*CacheEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	oidcEntries, ok := c.entries[api.AuthenticationMethodOIDC]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "Identity with OIDC subject %q not found", subject)
	}

	for _, entry := range oidcEntries {
		if entry == nil {
			continue
		}

		if entry.Subject == subject {
			entryCopy := *entry
			return &entryCopy, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Identity with OIDC subject %q not found", subject)
}

// GetIdentityProviderGroupMapping returns the auth groups that the given identity provider group maps to or an
// api.StatusError with http.StatusNotFound.
func (c *Cache) GetIdentityProviderGroupMapping(idpGroup string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	lxdGroups, ok := c.identityProviderGroups[idpGroup]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "No mapping found for identity provider group %q", idpGroup)
	}

	if lxdGroups == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "No mapping found for identity provider group %q", idpGroup)
	}

	return *lxdGroups, nil
}
