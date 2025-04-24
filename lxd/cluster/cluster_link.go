package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// GetClusterLinkCertificate retrieves a valid cluster certificate by contacting all specified addresses in parallel.
//
// It queries all addresses and ensures that:
//   - All retrieved certificates are identical.
//   - The certificate fingerprint matches the provided fingerprint.
//
// If a valid, consistent cluster certificate is found, it is returned with the first address at which it was found at. Otherwise, an error is returned.
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
			clusterAddress, err := validateAddress(addr)
			if err != nil {
				return err
			}

			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(clusterAddress, userAgent)
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
			return nil, "", errors.New("Mismatched certificates received from cluster addresses")
		}
	}

	return firstCert, firstAddress, nil
}

// ConnectClusterLink is a convenience function around [lxd.ConnectLXD] that configures the client with the correct parameters for cluster-to-cluster communication.
func ConnectClusterLink(ctx context.Context, s *state.State, clusterLink api.ClusterLink) (lxd.InstanceServer, error) {
	var lastErr error

	clusterCert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return nil, err
	}

	// Randomly shuffle the addresses to avoid always trying the same address first.
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	addresses := strings.Split(clusterLink.Config["volatile.addresses"], ",")
	r.Shuffle(len(addresses), func(i, j int) {
		addresses[i], addresses[j] = addresses[j], addresses[i]
	})

	var targetClient lxd.InstanceServer
	for _, address := range addresses {
		targetCert, _ := shared.GetRemoteCertificate("https://"+address, version.UserAgent)
		targetCertStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

		// Connect to cluster link.
		targetClient, err = lxd.ConnectLXD("https://"+address, &lxd.ConnectionArgs{
			TLSClientCert: string(clusterCert.PublicKey()),
			TLSClientKey:  string(clusterCert.PrivateKey()),
			TLSServerCert: targetCertStr,
			UserAgent:     version.UserAgent,
		})
		if err == nil {
			break
		}

		lastErr = fmt.Errorf("Failed to connect to %q: %w", address, err)
		logger.Warn(lastErr.Error())

		// Check if the context has been cancelled.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Context cancelled while trying addresses: %w", ctx.Err())
		default:
			// Continue to the next address.
		}
	}

	if targetClient == nil {
		return nil, fmt.Errorf("Failed to connect to any of the provided addresses: %w", lastErr)
	}

	return targetClient, nil
}

func validateAddress(addr string) (string, error) {
	clusterAddress := util.CanonicalNetworkAddress(addr, shared.HTTPSDefaultPort)
	u, err := url.Parse("https://" + clusterAddress)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("Invalid URL for address %q: %w", addr, err)
	}

	host := u.Hostname()
	ips := []net.IP{}
	ip := net.ParseIP(host)
	if ip != nil {
		ips = append(ips, ip)
	} else {
		resolvedIPs, err := net.LookupIP(host)
		if err != nil {
			return "", fmt.Errorf("Failed to resolve host %q: %w", host, err)
		}

		ips = resolvedIPs
	}

	for _, ip := range ips {
		if ip.IsLinkLocalUnicast() {
			return "", fmt.Errorf("Refusing to connect to restricted IP %q", ip)
		}
	}

	return u.String(), nil
}
