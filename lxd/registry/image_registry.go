package registry

import (
	"context"
	"encoding/pem"
	"fmt"
	"time"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ConnectImageRegistry is a convenience function that connects to the image registry's underlying image server based on its protocol and authentication requirements.
// It returns an initialized [client.ImageServer] ready for use.
func ConnectImageRegistry(ctx context.Context, s *state.State, imageRegistry api.ImageRegistry) (lxd.ImageServer, error) {
	// getRemoteCert retrieves the remote certificate from the image server.
	getRemoteCert := func() ([]byte, error) {
		remoteCert, err := shared.GetRemoteCertificate(context.Background(), imageRegistry.URL, version.UserAgent)
		if err != nil {
			return nil, err
		}

		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remoteCert.Raw}), nil
	}

	var imageServer lxd.ImageServer
	var remoteCert []byte
	var err error

	switch imageRegistry.Protocol {
	case api.ImageRegistryProtocolSimpleStreams:
		// Connect to the SimpleStreams image server.
		imageServer, err = lxd.ConnectSimpleStreams(imageRegistry.URL, &lxd.ConnectionArgs{
			UserAgent:   version.UserAgent,
			Proxy:       s.Proxy,
			CachePath:   s.OS.CacheDir,
			CacheExpiry: time.Hour,
		})

	case api.ImageRegistryProtocolLXD:
		if imageRegistry.Public {
			// Retrieve the remote certificate to connect to the image server.
			remoteCert, err = getRemoteCert()
			if err != nil {
				return nil, fmt.Errorf("Failed getting remote certificate for image registry %q: %w", imageRegistry.Name, err)
			}

			// Connect to the public LXD image server.
			imageServer, err = lxd.ConnectPublicLXD(imageRegistry.URL, &lxd.ConnectionArgs{
				TLSServerCert: string(remoteCert),
				UserAgent:     version.UserAgent,
				Proxy:         s.Proxy,
				CachePath:     s.OS.CacheDir,
				CacheExpiry:   time.Hour,
			})
		} else {
			var clusterLink *api.ClusterLink

			// Get the cluster link information.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				dbClusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), imageRegistry.Cluster)
				if err != nil {
					return err
				}

				clusterLink, err = dbClusterLink.ToAPI(ctx, tx.Tx())
				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed fetching cluster link %q for image registry %q: %w", imageRegistry.Cluster, imageRegistry.Name, err)
			}

			// Connect to the private LXD image server using the cluster link.
			imageServer, err = cluster.ConnectClusterLink(ctx, s, *clusterLink)
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
			imageServer = server.UseProject(imageRegistry.SourceProject)
		}
	}

	return imageServer, nil
}
