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

	lxd "github.com/canonical/lxd/client"
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
				return fmt.Errorf("Failed to retrieve certificate from %q: %w", addr, err)
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

// ConnectClusterLinkResult contains the result of trying to connect to a single cluster link address.
type ConnectClusterLinkResult struct {
	Address string
	Client  lxd.InstanceServer
	Error   error
}

// ConnectClusterLink is a convenience function around [lxd.ConnectLXD] that configures the client with the correct parameters for cluster-to-cluster communication.
// It attempts to connect to all addresses and returns the results, along with a client from a successful connection.
func ConnectClusterLink(ctx context.Context, s *state.State, clusterLink api.ClusterLink) (lxd.InstanceServer, []ConnectClusterLinkResult, error) {
	clusterCert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return nil, nil, err
	}

	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)
	resultsCh := make(chan ConnectClusterLinkResult, len(addresses))

	var wg sync.WaitGroup
	for _, address := range addresses {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			result := ConnectClusterLinkResult{Address: addr}

			// Try to retrieve the remote certificate.
			targetCert, err := shared.GetRemoteCertificate(ctx, "https://"+addr, version.UserAgent)
			if err != nil {
				result.Error = fmt.Errorf("Failed to get remote certificate from %q: %w", addr, err)
				resultsCh <- result
				return
			}

			targetCertStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

			// Connect to cluster link.
			client, err := lxd.ConnectLXD("https://"+addr, &lxd.ConnectionArgs{
				TLSClientCert: string(clusterCert.PublicKey()),
				TLSClientKey:  string(clusterCert.PrivateKey()),
				TLSServerCert: targetCertStr,
				UserAgent:     version.UserAgent,
			})
			if err != nil {
				result.Error = fmt.Errorf("Failed to connect to %q: %w", addr, err)
				resultsCh <- result
				return
			}

			result.Client = client
			resultsCh <- result
		}(address)
	}

	wg.Wait()
	close(resultsCh)

	connectResults := make([]ConnectClusterLinkResult, 0, len(addresses))
	var targetClient lxd.InstanceServer
	for result := range resultsCh {
		connectResults = append(connectResults, result)
		if result.Error == nil {
			if targetClient == nil {
				targetClient = result.Client
			}
		} else {
			logger.Warn("Failed to connect to cluster link address", logger.Ctx{"address": result.Address, "err": result.Error})
		}
	}

	if targetClient == nil {
		logger.Error("Failed to connect to any of the provided addresses")
		return nil, connectResults, errors.New("Failed to connect to any of the provided addresses")
	}

	return targetClient, connectResults, nil
}

// UpdateClusterLinkVolatileAddresses updates the volatile addresses of a cluster link. If the addresses have changed, [CheckClusterLinkCertificate] is called to ensure the cluster certificate remains valid.
func UpdateClusterLinkVolatileAddresses(ctx context.Context, s *state.State, clusterLink api.ClusterLink) error {
	targetClient, _, err := ConnectClusterLink(ctx, s, clusterLink)
	if err != nil {
		return fmt.Errorf("Failed to connect to target cluster link: %w", err)
	}

	addresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)

	// Update "volatile.addresses".
	targetClusterMembers, err := targetClient.GetClusterMembers()
	if err != nil {
		return fmt.Errorf("Failed to get cluster members from target cluster: %w", err)
	}

	newAddresses := make([]string, 0, len(targetClusterMembers))
	for _, clusterMember := range targetClusterMembers {
		newAddress := strings.TrimPrefix(clusterMember.URL, "https://")
		newAddresses = append(newAddresses, newAddress)
	}

	changed := !shared.EqualSets(addresses, newAddresses)
	if changed {
		newConfig := clusterLink.Config
		newConfig["volatile.addresses"] = strings.Join(newAddresses, ",")
		client, err := lxd.ConnectLXDUnix("", nil)
		if err != nil {
			return fmt.Errorf("Failed to connect to local LXD: %w", err)
		}

		identity, _, err := client.GetIdentity(api.AuthenticationMethodTLS, clusterLink.Name)
		if err != nil {
			return fmt.Errorf("Failed to get cluster link identity: %w", err)
		}

		// Validate the cluster link certificate against the new addresses.
		_, _, err = CheckClusterLinkCertificate(ctx, newAddresses, identity.Identifier, version.UserAgent)
		if err != nil {
			return fmt.Errorf("Failed to validate cluster link certificate: %w", err)
		}

		// Update cluster link configuration with new addresses.
		err = client.UpdateClusterLink(clusterLink.Name, api.ClusterLinkPut{Config: newConfig}, "")
		if err != nil {
			return err
		}
	}

	return nil
}
