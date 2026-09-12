package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
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
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
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

			// Confirm the address is actually serving the LXD API before trusting its certificate.
			err = VerifyClusterLinkServer(ctx, networkAddress, cert, userAgent)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("Failed verifying %q is a LXD server: %w", address, err))
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

// VerifyClusterLinkServer confirms that the given address is actually serving the LXD API by connecting
// with the pinned certificate (without presenting a client certificate) and querying the /1.0 endpoint.
// This guards against pinning the certificate of an arbitrary HTTPS server that isn't a LXD server.
func VerifyClusterLinkServer(ctx context.Context, address string, cert *x509.Certificate, userAgent string) error {
	certStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

	// SkipGetServer avoids the implicit /1.0 request ConnectLXDWithContext would otherwise make, so
	// the explicit query below is the only round trip and its error message is the one reported.
	client, err := lxd.ConnectLXDWithContext(ctx, "https://"+address, &lxd.ConnectionArgs{
		TLSServerCert: certStr,
		UserAgent:     userAgent,
		SkipGetServer: true,
	})
	if err != nil {
		return fmt.Errorf("Failed connecting to %q: %w", address, err)
	}

	// CheckClusterLinkCertificate calls this once per address, so leaving the transport open would
	// accumulate idle connections on every address refresh cycle.
	defer client.Disconnect()

	_, _, err = client.GetServer()
	if err != nil {
		return fmt.Errorf("Failed retrieving server information from %q: %w", address, err)
	}

	return nil
}

// GetNextMemberClient starts a connection attempt against every given address in parallel and returns
// a generator handing out the connected clients in the order in which they became available.
//
// The first call to the generator returns as soon as any address connects, without waiting for the
// slower attempts. Those attempts are kept running, so a subsequent call (used to retry an operation
// against another cluster member) hands out the next client without opening any new connection. Each
// client is returned at most once. Once no client is left the generator returns an error which wraps
// the failures of all addresses that could not be connected to.
//
// The returned cleanup function must be called once the caller is done with every handed out client.
// It aborts the outstanding attempts and disconnects all clients, including the ones already returned.
func GetNextMemberClient(ctx context.Context, addresses []string, args *lxd.ConnectionArgs) (next func() (lxd.InstanceServer, string, error), cleanup func()) {
	type connectionResult struct {
		client  lxd.InstanceServer
		address string
		err     error
	}

	// Each attempt shares one cancelable context which is only canceled by the cleanup function, as the
	// clients handed out by the generator keep using it for their subsequent requests.
	connCtx, cancel := context.WithCancel(ctx)

	resultCh := make(chan connectionResult, len(addresses))
	for _, address := range addresses {
		go func() {
			fullAddress := "https://" + util.CanonicalNetworkAddress(address, shared.HTTPSDefaultPort)
			client, err := lxd.ConnectLXDWithContext(connCtx, fullAddress, args)
			if err != nil {
				resultCh <- connectionResult{address: address, err: fmt.Errorf("Failed connecting to remote cluster address %q: %w", address, err)}
				return
			}

			resultCh <- connectionResult{address: address, client: client}
		}()
	}

	var mu sync.Mutex
	var handedOut []lxd.InstanceServer
	var errs []error
	pending := len(addresses)

	next = func() (lxd.InstanceServer, string, error) {
		mu.Lock()
		defer mu.Unlock()

		for pending > 0 {
			result := <-resultCh
			pending--

			if result.err != nil {
				errs = append(errs, result.err)
				continue
			}

			handedOut = append(handedOut, result.client)

			return result.client, result.address, nil
		}

		var zero lxd.InstanceServer
		err := errors.New("Failed connecting to any remaining cluster member")
		if len(errs) > 0 {
			err = fmt.Errorf("%w: %w", err, errors.Join(errs...))
		}

		return zero, "", err
	}

	cleanup = func() {
		cancel()

		mu.Lock()
		defer mu.Unlock()

		for _, client := range handedOut {
			client.Disconnect()
		}

		handedOut = nil

		remaining := pending
		pending = 0

		// Drain the attempts that were never handed out so that any client which connected in the
		// meantime gets disconnected instead of being leaked. This runs in the background because an
		// attempt may still take a moment to notice its canceled context.
		go func() {
			for range remaining {
				result := <-resultCh
				if result.err == nil {
					result.client.Disconnect()
				}
			}
		}()
	}

	return next, cleanup
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

// GetPublicClusterLinkConnectionArgs builds connection args for public cluster links.
// No client certificate is presented; only the server certificate is pinned.
func GetPublicClusterLinkConnectionArgs(targetCert *x509.Certificate) *lxd.ConnectionArgs {
	targetCertStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	return &lxd.ConnectionArgs{
		TLSServerCert: targetCertStr,
		UserAgent:     version.UserAgent,
	}
}

// LoadClusterLinkAndCert loads a cluster link by name and returns its database ID, API representation, and the parsed TLS certificate.
// For bidirectional links the certificate is loaded via the associated identity; for links with no associated identity
// (unidirectional and public) it is loaded from cluster_links_certificates.
func LoadClusterLinkAndCert(ctx context.Context, tx *sql.Tx, name string) (id int64, clusterLink *api.ClusterLink, cert *x509.Certificate, err error) {
	dbLink, err := dbCluster.GetClusterLink(ctx, tx, name)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("Failed loading cluster link %q: %w", name, err)
	}

	config, err := dbCluster.ClusterLinksConfigStore().GetByEntityIDs(ctx, tx, dbLink.ID)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("Failed loading cluster link config: %w", err)
	}

	clusterLink = dbLink.ToAPI(config)

	var pemCert string
	if dbLink.IdentityID != nil {
		// Bidirectional: cert is stored via the identity.
		identity, err := dbCluster.GetIdentityByID(ctx, tx, *dbLink.IdentityID)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("Failed loading cluster link identity: %w", err)
		}

		certs, err := dbCluster.GetIdentitiesPEMCertificates(ctx, tx, &identity.ID)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("Failed loading cluster link certificate: %w", err)
		}

		if len(certs[identity.ID]) == 0 {
			return 0, nil, nil, fmt.Errorf("No certificate found for cluster link identity %q", identity.Name)
		}

		pemCert = certs[identity.ID][0]
	} else {
		// No associated identity (unidirectional or public): cert is stored directly in cluster_links_certificates.
		pemCert, err = dbCluster.GetClusterLinkPEMCertificate(ctx, tx, dbLink.ID)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("Failed loading cluster link certificate: %w", err)
		}
	}

	cert, err = shared.ParseCert([]byte(pemCert))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("Failed parsing certificate for cluster link %q: %w", dbLink.Name, err)
	}

	return dbLink.ID, clusterLink, cert, nil
}

// ConnectCluster connects to a linked cluster using the provided connection args, trying all addresses in parallel and returning the first successful connection.
func ConnectCluster(ctx context.Context, clusterLink api.ClusterLink, args *lxd.ConnectionArgs) (lxd.InstanceServer, error) {
	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("Failed connecting to any address of cluster link %q: no addresses available", clusterLink.Name)
	}

	type connectionResult struct {
		index  int
		client lxd.InstanceServer
		err    error
	}

	// Start a connection attempt to every address concurrently. Each attempt gets its own
	// cancelable context so the losing attempts can be aborted once one of them succeeds.
	// The winning client keeps its context as it is used for all of its subsequent requests.
	resultCh := make(chan connectionResult, len(addresses))
	cancels := make([]context.CancelFunc, len(addresses))
	for i, address := range addresses {
		connCtx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel

		go func() {
			client, err := lxd.ConnectLXDWithContext(connCtx, "https://"+address, args)
			if err != nil {
				resultCh <- connectionResult{index: i, err: fmt.Errorf("Failed connecting to %q: %w", address, err)}
				return
			}

			resultCh <- connectionResult{index: i, client: client}
		}()
	}

	// Return the first successful connection. The losing attempts are canceled and their
	// results drained in the background so that any client which connected in the meantime
	// gets disconnected instead of being leaked.
	var errs []error
	for i := range addresses {
		r := <-resultCh
		if r.err != nil {
			cancels[r.index]()
			errs = append(errs, r.err)
			continue
		}

		for j, cancel := range cancels {
			if j != r.index {
				cancel()
			}
		}

		remaining := len(addresses) - i - 1
		if remaining > 0 {
			go func() {
				for range remaining {
					extra := <-resultCh
					if extra.client != nil {
						extra.client.Disconnect()
					}
				}
			}()
		}

		return r.client, nil
	}

	return nil, fmt.Errorf("Failed connecting to any address of cluster link %q: %w", clusterLink.Name, errors.Join(errs...))
}

// RefreshClusterLinkVolatileAddresses refreshes the volatile addresses of a cluster link.
// It connects to the linked cluster and retrieves its current cluster members. If the addresses
// have changed, [CheckClusterLinkCertificate] is called to ensure the cluster certificate remains valid.
// If targetClient is non-nil it is used instead of establishing a new connection to the linked cluster.
// The caller retains ownership of targetClient and remains responsible for disconnecting it.
func RefreshClusterLinkVolatileAddresses(ctx context.Context, s *state.State, name string, targetClient lxd.InstanceServer) error {
	// Fetch the cluster link and identity cert in a single transaction so we have everything needed
	// for connecting and cert validation without any further DB queries.
	var clusterLink *api.ClusterLink
	var clusterLinkID int64
	var targetCert *x509.Certificate
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		clusterLinkID, clusterLink, targetCert, err = LoadClusterLinkAndCert(ctx, tx.Tx(), name)
		return err
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

	if targetClient == nil {
		var args *lxd.ConnectionArgs
		if api.ClusterLinkTypePresentsClientCertificate(clusterLink.Type) {
			clusterCert := s.Endpoints.NetworkCert()
			args = GetClusterLinkConnectionArgs(clusterCert, targetCert)
		} else {
			args = GetPublicClusterLinkConnectionArgs(targetCert)
		}

		var err error
		targetClient, err = ConnectCluster(ctx, *clusterLink, args)
		if err != nil {
			return fmt.Errorf("Failed connecting to target cluster link: %w", err)
		}

		defer targetClient.Disconnect()
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
		return dbCluster.ClusterLinksConfigStore().Set(ctx, tx.Tx(), clusterLinkID, clusterLink.Config)
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
