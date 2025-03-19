package clusterlinks

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"sync"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// GetClusterLinkCertificate retrieves a valid cluster certificate by contacting multiple addresses concurrently.
//
// If override addresses are provided, it attempts to fetch the certificate from those addresses.
// Otherwise, it queries all addresses listed in the join token concurrently and ensures that:
//   - All retrieved certificates are identical.
//   - The certificate fingerprint matches the join token fingerprint.
//
// If a valid, consistent cluster certificate is found, it is returned. Otherwise, an error is returned.
func GetClusterLinkCertificate(ctx context.Context, joinToken *api.CertificateAddToken, overrideAddresses []string, userAgent string) (*x509.Certificate, error) {
	type result struct {
		cert    *x509.Certificate
		err     error
		address string
	}

	var addresses []string
	if overrideAddresses != nil {
		addresses = overrideAddresses
	} else {
		addresses = joinToken.Addresses
	}

	resCh := make(chan result, len(addresses))

	var wg sync.WaitGroup
	for _, address := range addresses {
		wg.Add(1)
		// Launch a goroutine for each address
		go func(addr string) {
			defer wg.Done()

			clusterAddress := util.CanonicalNetworkAddress(addr, shared.HTTPSDefaultPort)
			u, err := url.Parse("https://" + clusterAddress)
			if err != nil || u.Host == "" {
				return
			}

			select {
			case <-ctx.Done():
				resCh <- result{cert: nil, err: ctx.Err(), address: clusterAddress}
				return
			default:
			}

			// Try to retrieve the remote certificate
			cert, err := shared.GetRemoteCertificate(u.String(), userAgent)
			if err != nil {
				resCh <- result{cert: nil, err: fmt.Errorf("failed to retrieve certificate from %q: %w", clusterAddress, err), address: clusterAddress}
				return
			}

			// Check that the certificate fingerprint matches the token
			certDigest := shared.CertFingerprint(cert)
			if joinToken.Fingerprint != certDigest {
				resCh <- result{cert: nil, err: fmt.Errorf("certificate fingerprint mismatch for address %q", clusterAddress), address: clusterAddress}
				return
			}

			// Return the valid certificate
			resCh <- result{cert: cert, err: nil, address: clusterAddress}
		}(address)
	}

	// Close the result channel when all goroutines complete or context is done
	go func() {
		wgDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(wgDone)
		}()

		select {
		case <-wgDone:
			// All goroutines completed normally
		case <-ctx.Done():
			// Context was cancelled
		}

		close(resCh)
	}()

	var lastErr error
	var firstCert *x509.Certificate
	for res := range resCh {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if res.err != nil {
			lastErr = res.err
			continue
		}

		if res.cert != nil {
			if firstCert == nil {
				firstCert = res.cert
			} else if !firstCert.Equal(res.cert) {
				return nil, fmt.Errorf("mismatched certificates received from cluster addresses")
			}
		}
	}

	if firstCert == nil {
		if lastErr != nil {
			return nil, fmt.Errorf("unable to connect to any of the cluster members specified in join token: %w", lastErr)
		}

		return nil, fmt.Errorf("unable to connect to any of the cluster members specified in join token")
	}

	return firstCert, nil
}
