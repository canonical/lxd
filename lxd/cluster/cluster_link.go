package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
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

// RefreshClusterLinkVolatileAddresses refreshes the volatile addresses of a cluster link.
// It connects to the linked cluster and retrieves its current cluster members. If the addresses
// have changed, [CheckClusterLinkCertificate] is called to ensure the cluster certificate remains valid.
func RefreshClusterLinkVolatileAddresses(ctx context.Context, s *state.State, name string) error {
	// Fetch the cluster link and identity cert in a single transaction so we have everything needed
	// for connecting and cert validation without any further DB queries.
	var clusterLink *api.ClusterLink
	var clusterLinkID int64
	var targetCert *x509.Certificate
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link: %w", err)
		}

		clusterLinkID = dbLink.ID

		config, err := dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), &dbLink.ID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link config: %w", err)
		}

		clusterLink = dbLink.ToAPI(config)

		identity, err := dbCluster.GetIdentityByID(ctx, tx.Tx(), dbLink.IdentityID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link identity: %w", err)
		}

		certs, err := dbCluster.GetIdentitiesPEMCertificates(ctx, tx.Tx(), &identity.ID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link certificate: %w", err)
		}

		if len(certs[identity.ID]) == 0 {
			return fmt.Errorf("No certificate found for cluster link identity %q", identity.Name)
		}

		certBlock, _ := pem.Decode([]byte(certs[identity.ID][0]))
		if certBlock == nil {
			return fmt.Errorf("Failed decoding certificate for cluster link identity %q", identity.Name)
		}

		targetCert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return fmt.Errorf("Failed extracting certificate from cluster link identity: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, true)
	if len(addresses) == 0 {
		// Pending or otherwise incomplete cluster links do not have bootstrap addresses yet,
		// so there is nothing to refresh and we should avoid logging connection failures.
		return nil
	}

	clusterCert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return err
	}

	targetClient, err := ConnectCluster(ctx, *clusterLink, GetClusterLinkConnectionArgs(clusterCert, targetCert))
	if err != nil {
		return fmt.Errorf("Failed connecting to target cluster link: %w", err)
	}

	// Get cluster members from the target cluster.
	targetClusterMembers, err := targetClient.GetClusterMembers()
	if err != nil {
		return fmt.Errorf("Failed getting cluster members from target cluster: %w", err)
	}

	newAddresses := make([]string, 0, len(targetClusterMembers))
	for _, clusterMember := range targetClusterMembers {
		if clusterMember.URL == "" {
			continue
		}

		newAddresses = append(newAddresses, strings.TrimPrefix(clusterMember.URL, "https://"))
	}

	if !addressSetChanged(addresses, newAddresses) {
		return nil
	}

	// Validate the cluster link certificate against the new addresses using the cert we already hold.
	_, _, err = CheckClusterLinkCertificate(ctx, newAddresses, shared.CertFingerprint(targetCert), version.UserAgent)
	if err != nil {
		return fmt.Errorf("Failed validating cluster link certificate: %w", err)
	}

	clusterLink.Config["volatile.addresses"] = strings.Join(newAddresses, ",")

	// Update the cluster link config in the database.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.UpdateClusterLinkConfig(ctx, tx.Tx(), clusterLinkID, clusterLink.Config)
	})
	if err != nil {
		return fmt.Errorf("Failed updating cluster link config: %w", err)
	}

	return nil
}

// addressSetChanged returns true if the two address slices differ in their set of values, regardless of order.
func addressSetChanged(current []string, updated []string) bool {
	if len(current) != len(updated) {
		return true
	}

	currentSet := make(map[string]struct{}, len(current))
	for _, addr := range current {
		currentSet[addr] = struct{}{}
	}

	for _, addr := range updated {
		_, ok := currentSet[addr]
		if !ok {
			return true
		}
	}

	return false
}
