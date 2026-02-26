package registry

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ConnectImageRegistry is a convenience function that connects to the image registry's underlying image server based on its protocol and authentication requirements.
// It returns an initialized [client.ImageServer] ready for use.
func ConnectImageRegistry(ctx context.Context, s *state.State, imageRegistry api.ImageRegistry) (lxd.ImageServer, error) {
	var imageServer lxd.ImageServer
	var err error

	registryURL := imageRegistry.Config["url"]
	registryCluster := imageRegistry.Config["cluster"]
	registrySourceProject := imageRegistry.Config["source_project"]

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

		// Build the connection arguments based on the cluster link type. Public cluster links present
		// no client certificate and rely solely on the pinned server certificate; other link types
		// present the local cluster certificate.
		var connArgs *lxd.ConnectionArgs
		if clusterLink.Type == api.ClusterLinkTypePublic {
			connArgs = cluster.GetPublicClusterLinkConnectionArgs(targetCert)
		} else {
			clusterCert := s.Endpoints.NetworkCert()
			connArgs = cluster.GetClusterLinkConnectionArgs(clusterCert, targetCert)
		}

		// Connect to the LXD image server using the cluster link.
		imageServer, err = cluster.ConnectCluster(ctx, *clusterLink, connArgs)

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
