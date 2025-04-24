package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
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

// GetClusterLinkConnectionArgs builds connection args for cluster-to-cluster communication.
func GetClusterLinkConnectionArgs(clusterCert *shared.CertInfo, targetCert *x509.Certificate) *lxd.ConnectionArgs {
	targetCertStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	return &lxd.ConnectionArgs{
		TLSClientCert: string(clusterCert.PublicKey()),
		TLSClientKey:  string(clusterCert.PrivateKey()),
		TLSServerCert: targetCertStr,
		UserAgent:     version.UserAgent,
	}
}

// ConnectCluster connects to a linked cluster using the provided connection args, trying each address until one succeeds.
func ConnectCluster(ctx context.Context, clusterLink api.ClusterLink, args *lxd.ConnectionArgs) (lxd.InstanceServer, error) {
	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)
	var errs []error
	for _, address := range addresses {
		client, err := lxd.ConnectLXD("https://"+address, args)
		if err != nil {
			errs = append(errs, fmt.Errorf("Failed connecting to %q: %w", address, err))
			continue
		}

		return client, nil
	}

	return nil, fmt.Errorf("Failed connecting to any address of cluster link %q: %w", clusterLink.Name, errors.Join(errs...))
}
