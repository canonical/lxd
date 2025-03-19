package cluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"sync"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

// GetClusterLinkCertificate retrieves a valid cluster certificate by contacting all specified IP addresses concurrently.
//
// It concurrently queries all IP addresses and ensures that:
//   - All retrieved certificates are identical.
//   - The certificate fingerprint matches the provided fingerprint.
//
// If a valid, consistent cluster certificate is found, it is returned with the first address it was found at. Otherwise, an error is returned.
func GetClusterLinkCertificate(ctx context.Context, addresses []string, fingerprint string, userAgent string) (*x509.Certificate, string, error) {
	type result struct {
		cert    *x509.Certificate
		address string
		err     error
	}

	resCh := make(chan result, len(addresses))

	var wg sync.WaitGroup
	for _, address := range addresses {
		wg.Add(1)
		// Launch a goroutine for each address.
		go func(addr string) {
			defer wg.Done()

			ip := net.ParseIP(addr)
			if ip == nil {
				resCh <- result{cert: nil, err: fmt.Errorf("Invalid IP address %q", addr), address: addr}
			}

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

			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(u.String(), userAgent)
			if err != nil {
				resCh <- result{cert: nil, err: fmt.Errorf("Failed to retrieve certificate from %q: %w", clusterAddress, err), address: clusterAddress}
				return
			}

			// Check that the certificate fingerprint matches the provided fingerprint.
			certDigest := shared.CertFingerprint(cert)
			if fingerprint != certDigest {
				resCh <- result{cert: nil, err: fmt.Errorf("Certificate fingerprint mismatch for address %q", clusterAddress), address: clusterAddress}
				return
			}

			// Return the valid certificate.
			resCh <- result{cert: cert, err: nil, address: clusterAddress}
		}(address)
	}

	// Close the result channel when all goroutines complete or context is done.
	go func() {
		wgDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(wgDone)
		}()

		select {
		case <-wgDone:
			// All goroutines completed normally.
		case <-ctx.Done():
			// Context was cancelled.
		}

		close(resCh)
	}()

	var lastErr error
	var firstCert *x509.Certificate
	var firstAddress string
	for res := range resCh {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		if res.err != nil {
			lastErr = res.err
			continue
		}

		if res.cert != nil {
			if firstCert == nil {
				firstCert = res.cert
				firstAddress = res.address
			} else if !firstCert.Equal(res.cert) {
				return nil, "", fmt.Errorf("Mismatched certificates received from cluster addresses")
			}
		}
	}

	if firstCert == nil {
		if lastErr != nil {
			return nil, "", fmt.Errorf("Unable to connect to any of the cluster members: %w", lastErr)
		}

		return nil, "", fmt.Errorf("Unable to connect to any of the cluster members")
	}

	return firstCert, firstAddress, nil
}
