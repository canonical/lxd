package cluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"

	"golang.org/x/sync/errgroup"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

// GetClusterLinkCertificate retrieves a valid cluster certificate by contacting all specified addresses in parallel.
//
// It queries all addresses and ensures that:
//   - All retrieved certificates are identical.
//   - The certificate fingerprint matches the provided fingerprint.
//
// If a valid, consistent cluster certificate is found, it is returned with the first address it was found at. Otherwise, an error is returned.
func GetClusterLinkCertificate(ctx context.Context, addresses []string, fingerprint string, userAgent string) (*x509.Certificate, string, error) {
	type result struct {
		cert    *x509.Certificate
		address string
	}

	// Pass parent context to the goroutines.
	g, gCtx := errgroup.WithContext(ctx)

	// Create a buffered channel to collect results from goroutines.
	resCh := make(chan result, len(addresses))

	for _, address := range addresses {
		addr := address
		// Launch a goroutine for each address.
		g.Go(func() error {
			clusterAddress := util.CanonicalNetworkAddress(addr, shared.HTTPSDefaultPort)
			u, err := url.Parse("https://" + clusterAddress)
			if err != nil || u.Host == "" {
				return fmt.Errorf("Invalid URL for address %q: %w", addr, err)
			}

			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(u.String(), userAgent)
			if err != nil {
				return fmt.Errorf("Failed to retrieve certificate from %q: %w", clusterAddress, err)
			}

			// Check that the certificate fingerprint matches the provided fingerprint.
			certDigest := shared.CertFingerprint(cert)
			if fingerprint != certDigest {
				return fmt.Errorf("Certificate fingerprint mismatch for address %q", clusterAddress)
			}

			// Return the valid certificate.
			select {
			case <-gCtx.Done():
				return gCtx.Err()
			case resCh <- result{cert: cert, address: clusterAddress}:
				return nil
			}
		})
	}

	err := g.Wait()
	close(resCh)
	if err != nil {
		return nil, "", err
	}

	var firstCert *x509.Certificate
	var firstAddress string
	// Iterate over the results and check for consistency.
	for res := range resCh {
		if firstCert == nil {
			firstCert = res.cert
			firstAddress = res.address
		} else if !firstCert.Equal(res.cert) {
			return nil, "", fmt.Errorf("Mismatched certificates received from cluster addresses")
		}
	}

	return firstCert, firstAddress, nil
}
