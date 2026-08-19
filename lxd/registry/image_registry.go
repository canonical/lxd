package registry

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ConnectImageRegistry is a convenience function that connects to the image registry's underlying image server based on its protocol and authentication requirements.
// It returns an initialized [client.ImageServer] ready for use.
func ConnectImageRegistry(ctx context.Context, s *state.State, imageRegistry api.ImageRegistry) (lxd.ImageServer, error) {
	// getRemoteCert retrieves the remote certificate from the image server.
	getRemoteCert := func(url string) ([]byte, error) {
		remoteCert, err := shared.GetRemoteCertificate(ctx, url, version.UserAgent)
		if err != nil {
			return nil, err
		}

		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remoteCert.Raw}), nil
	}

	var imageServer lxd.ImageServer
	var remoteCert []byte
	var err error

	registryURL := imageRegistry.Config["url"]
	registryCluster := imageRegistry.Config["cluster"]
	registrySourceProject := imageRegistry.Config["source_project"]
	registryPublic := imageRegistry.Public

	switch imageRegistry.Protocol {
	case api.ImageRegistryProtocolSimpleStreams:
		// Connect to the SimpleStreams image server.
		imageServer, err = lxd.ConnectSimpleStreams(registryURL, &lxd.ConnectionArgs{
			UserAgent:   version.UserAgent,
			Proxy:       s.Proxy,
			CachePath:   s.OS.CacheDir,
			CacheExpiry: time.Hour,
		})

	case api.ImageRegistryProtocolLXD:
		if registryPublic {
			// Retrieve the remote certificate to connect to the image server.
			remoteCert, err = getRemoteCert(registryURL)
			if err != nil {
				return nil, fmt.Errorf("Failed loading remote certificate for image registry %q: %w", imageRegistry.Name, err)
			}

			// Connect to the public LXD image server.
			imageServer, err = lxd.ConnectPublicLXD(registryURL, &lxd.ConnectionArgs{
				TLSServerCert: string(remoteCert),
				UserAgent:     version.UserAgent,
				Proxy:         s.Proxy,
				CachePath:     s.OS.CacheDir,
				CacheExpiry:   time.Hour,
			})
		} else {
			var clusterLink *api.ClusterLink
			var targetCert *x509.Certificate

			// Get the cluster link information.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, clusterLink, targetCert, err = cluster.LoadClusterLinkAndCert(ctx, tx.Tx(), registryCluster)
				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed loading cluster link information %q for image registry %q: %w", registryCluster, imageRegistry.Name, err)
			}

			// Load the cluster certificate.
			clusterCert := s.Endpoints.NetworkCert()

			// Connect to the private LXD image server using the cluster link.
			connArgs := cluster.GetClusterLinkConnectionArgs(clusterCert, targetCert)
			imageServer, err = cluster.ConnectCluster(ctx, *clusterLink, connArgs)
		}

	default:
		return nil, fmt.Errorf("Unknown image registry protocol %q for image registry %q", imageRegistry.Protocol, imageRegistry.Name)
	}

	// Check the error from the connection attempt.
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to image registry %q: %w", imageRegistry.Name, err)
	}

	// Use the source project for the LXD image registry.
	if imageRegistry.Protocol == api.ImageRegistryProtocolLXD {
		server, ok := imageServer.(lxd.InstanceServer)
		if ok {
			imageServer = server.UseProject(registrySourceProject)
		}
	}

	return imageServer, nil
}
