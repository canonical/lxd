package identity

import (
	"crypto/x509"
	"net/http"
	"slices"
	"sync"

	"github.com/canonical/lxd/shared/api"
)

// Cache represents a thread-safe in-memory cache of the credentials of identities in the database.
//
// Certificates are keyed on the certificate fingerprint. Secrets are keyed on the bearer identity identifier.
// It is necessary to separate server, client and metrics certificates because of their different handling during authentication.
// For example, metrics certificates are not considered for authentication unless the API route is under /1.0/metrics.
// Additionally, it is crucial that authentication can identify server certificates without a database call (because
// establishing a database connection requires authentication).
type Cache struct {
	serverCertificates      map[string]*x509.Certificate
	serverCertificatesMu    sync.RWMutex
	clientCertificates      map[string]*x509.Certificate
	clientCertificatesMu    sync.RWMutex
	metricsCertificates     map[string]*x509.Certificate
	metricsCertificatesMu   sync.RWMutex
	bearerIdentitySecrets   map[string][]byte
	bearerIdentitySecretsMu sync.RWMutex
}

// GetServerCertificates returns matching server certificates.
func (c *Cache) GetServerCertificates(fingerprints ...string) map[string]x509.Certificate {
	return getCerts(&c.serverCertificatesMu, c.serverCertificates, fingerprints...)
}

// GetClientCertificates returns matching client certificates.
func (c *Cache) GetClientCertificates(fingerprints ...string) map[string]x509.Certificate {
	return getCerts(&c.clientCertificatesMu, c.clientCertificates, fingerprints...)
}

// GetMetricsCertificates returns matching metrics certificates.
func (c *Cache) GetMetricsCertificates(fingerprints ...string) map[string]x509.Certificate {
	return getCerts(&c.metricsCertificatesMu, c.metricsCertificates, fingerprints...)
}

func getCerts(mu *sync.RWMutex, m map[string]*x509.Certificate, fingerprints ...string) map[string]x509.Certificate {
	mu.RLock()
	defer mu.RUnlock()

	out := make(map[string]x509.Certificate, len(m))
	for k, v := range m {
		if len(fingerprints) == 0 || slices.Contains(fingerprints, k) {
			out[k] = *v
		}
	}

	return out
}

// GetSecret returns the secret of a bearer identity by their UUID.
func (c *Cache) GetSecret(bearerIdentityUUID string) ([]byte, error) {
	c.bearerIdentitySecretsMu.RLock()
	defer c.bearerIdentitySecretsMu.RUnlock()
	secret, ok := c.bearerIdentitySecrets[bearerIdentityUUID]
	if !ok {
		return nil, api.NewStatusError(http.StatusNotFound, "No secret found for bearer token identity")
	}

	return secret, nil
}

// ReplaceAll deletes all credentials from the cache and replaces them with the given values.
func (c *Cache) ReplaceAll(serverCerts map[string]*x509.Certificate, clientCerts map[string]*x509.Certificate, metricsCerts map[string]*x509.Certificate, secrets map[string][]byte) {
	c.bearerIdentitySecretsMu.Lock()
	defer c.bearerIdentitySecretsMu.Unlock()

	c.serverCertificates = serverCerts
	c.clientCertificates = clientCerts
	c.metricsCertificates = metricsCerts
	c.bearerIdentitySecrets = secrets
}
