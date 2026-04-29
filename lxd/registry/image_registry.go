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
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ConnectImageRegistry is a convenience function that connects to the image registry's underlying image server based on its protocol and authentication requirements.
// It returns an initialized [client.ImageServer] ready for use.
func ConnectImageRegistry(ctx context.Context, s *state.State, imageRegistry api.ImageRegistry) (lxd.ImageServer, error) {
	// getRemoteCert retrieves the remote certificate from the image server.
	getRemoteCert := func(url string) ([]byte, error) {
		remoteCert, err := shared.GetRemoteCertificate(context.Background(), url, version.UserAgent)
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
	registryPublic := shared.IsTrue(imageRegistry.Config["public"])

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
			var clusterCert *shared.CertInfo

			// Get the cluster link information.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				dbClusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
				if err != nil {
					return err
				}

				clusterLinkConfig, err := dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), &dbClusterLink.ID)
				if err != nil {
					return fmt.Errorf("Failed loading cluster link config: %w", err)
				}

				clusterLink = dbClusterLink.ToAPI(clusterLinkConfig)

				identity, err := dbCluster.GetIdentityByID(ctx, tx.Tx(), dbClusterLink.IdentityID)
				if err != nil {
					return fmt.Errorf("Failed load cluster link identity: %w", err)
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
				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed loading cluster link information %q for image registry %q: %w", registryCluster, imageRegistry.Name, err)
			}

			// Load the cluster certificate.
			clusterCert, err = util.LoadClusterCert(s.OS.VarDir)
			if err != nil {
				return nil, fmt.Errorf("Failed loading cluster certificate: %w", err)
			}

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
