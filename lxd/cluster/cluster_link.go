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

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// CheckClusterLinkCertificate checks the cluster certificate at each address and ensures they all match the provided fingerprint.
// If a valid, consistent cluster certificate is found, it is returned with the first address at which it was found at. Otherwise, an error is returned.
func CheckClusterLinkCertificate(ctx context.Context, addresses []string, fingerprint string, userAgent string) (*x509.Certificate, string, error) {
	type result struct {
		cert    *x509.Certificate
		address string
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

	var once sync.Once
	var firstResult result
	for _, address := range addresses {
		addr := address
		networkAddress := util.CanonicalNetworkAddress(addr, shared.HTTPSDefaultPort)
		u, err := url.Parse("https://" + networkAddress)
		if err != nil || u.Host == "" {
			return nil, "", fmt.Errorf("Invalid URL for address %q: %w", addr, err)
		}

		// Launch a goroutine for each address.
		g.Go(func() error {
			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(ctx, u.String(), userAgent)
			if err != nil {
				return fmt.Errorf("Failed retrieving certificate from %q: %w", addr, err)
			}

			// Check that the certificate fingerprint matches the provided fingerprint.
			certDigest := shared.CertFingerprint(cert)
			if fingerprint != certDigest {
				return fmt.Errorf("Certificate fingerprint mismatch for address %q", addr)
			}

			once.Do(func() {
				firstResult = result{cert: cert, address: addr}
			})
			return nil
		})
	}

	err := g.Wait()
	if err != nil {
		return nil, "", err
	}

	return firstResult.cert, firstResult.address, nil
}

// ConnectClusterLink is a convenience function around [lxd.ConnectLXD] that configures the client with the correct parameters for cluster-to-cluster communication.
// It attempts to connect to all addresses and returns the first successful client.
func ConnectClusterLink(ctx context.Context, s *state.State, clusterLink api.ClusterLink) (lxd.InstanceServer, error) {
	clusterCert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return nil, err
	}

	// Get the cluster link identity to retrieve the stored certificate.
	var targetCert *x509.Certificate
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		identity, err := dbCluster.GetIdentityByNameOrIdentifier(ctx, tx.Tx(), api.AuthenticationMethodTLS, clusterLink.Name)
		if err != nil {
			return fmt.Errorf("Failed fetching cluster link identity: %w", err)
		}

		targetCert, err = identity.X509()
		if err != nil {
			return fmt.Errorf("Failed extracting certificate from cluster link identity: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	targetCertStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)
	for _, address := range addresses {
		// Connect to cluster link.
		client, err := lxd.ConnectLXD("https://"+address, &lxd.ConnectionArgs{
			TLSClientCert: string(clusterCert.PublicKey()),
			TLSClientKey:  string(clusterCert.PrivateKey()),
			TLSServerCert: targetCertStr,
			UserAgent:     version.UserAgent,
		})
		if err != nil {
			logger.Warn("Failed connecting to cluster link address", logger.Ctx{"address": address, "err": err})
			continue
		}

		return client, nil
	}

	logger.Error("Failed connecting to any cluster link address", logger.Ctx{"clusterLink": clusterLink.Name})
	return nil, errors.New("Failed connecting to any cluster link address")
}
