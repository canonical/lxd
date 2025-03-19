package cluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

// CheckClusterLinkCertificate checks the cluster certificate at each address and ensures every reachable address matches the provided fingerprint.
// If a valid, consistent cluster certificate is found, it is returned with the first address at which it was found. Unreachable addresses are tolerated
// so long as at least one address is reachable and no reachable address presents a different certificate.
func CheckClusterLinkCertificate(ctx context.Context, addresses []string, fingerprint string, userAgent string) (*x509.Certificate, string, error) {
	type result struct {
		cert    *x509.Certificate
		address string
	}

	if len(addresses) == 0 {
		return nil, "", errors.New("Failed checking cluster link certificate: no addresses provided")
	}

	_, ok := ctx.Deadline()
	if !ok {
		// Set default timeout of 30s if no deadline context provided.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(30*time.Second))
		defer cancel()
	}

	// Pass context to the goroutines.
	g, ctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	var once sync.Once
	var firstResult result
	var errs []error
	for _, address := range addresses {
		networkAddress := util.CanonicalNetworkAddress(address, shared.HTTPSDefaultPort)
		u, err := url.Parse("https://" + networkAddress)
		if err != nil {
			return nil, "", fmt.Errorf("Invalid URL for address %q: %w", address, err)
		}

		if u.Host == "" {
			return nil, "", fmt.Errorf("Invalid URL for address %q: empty host", address)
		}

		// Launch a goroutine for each address.
		g.Go(func() error {
			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(ctx, u.String(), userAgent)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("Failed retrieving certificate from %q: %w", address, err))
				mu.Unlock()
				return nil
			}

			// Check that the certificate fingerprint matches the provided fingerprint.
			certDigest := shared.CertFingerprint(cert)
			if fingerprint != certDigest {
				mu.Lock()
				errs = append(errs, fmt.Errorf("Certificate fingerprint mismatch for address %q", address))
				mu.Unlock()
				return nil
			}

			once.Do(func() {
				firstResult = result{cert: cert, address: address}
			})
			return nil
		})
	}

	err := g.Wait()
	if err != nil {
		return nil, "", err
	}

	if firstResult.cert != nil {
		return firstResult.cert, firstResult.address, nil
	}

	return nil, "", fmt.Errorf("Failed retrieving cluster certificate from any address: %w", errors.Join(errs...))
}
