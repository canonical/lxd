package registry

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// transitionalSimpleStreamsHosts contains hostnames whose URLs are allowed
// to be auto-created as image registries through the deprecated Server/Protocol
// backward-compatibility path. Only HTTPS URLs on these hosts are accepted.
var transitionalSimpleStreamsHosts = []string{
	"cloud-images.ubuntu.com",
	"images.lxd.canonical.com",
	"cdimage.ubuntu.com",
}

// IsTransitionalSimpleStreamsURL reports whether raw URL is an endpoint eligible
// for the transitional image registry auto-creation path. The URL must use the
// HTTPS scheme and its hostname must apper in the allowlist.
func IsTransitionalSimpleStreamsURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	if u.Scheme != "https" {
		return false
	}

	host := strings.ToLower(u.Hostname())
	for _, allowed := range transitionalSimpleStreamsHosts {
		if host == allowed {
			return true
		}
	}

	return false
}

// TransitionalRegistryName derives a deterministic image registry name from a
// SimpleStreams URL. The scheme is stripped, the hostname is lower-cased, and
// non-empty path segments are joined with "-". The result is suitable for use
// as an image registry name (ASCII, no "/" or ":").
//
// Returns an empty string if rawURL cannot be parsed.
func TransitionalRegistryName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}

	// Collect non-empty path segments.
	var segments []string
	for _, seg := range strings.Split(u.Path, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}

	if len(segments) == 0 {
		return host
	}

	return host + "-" + strings.Join(segments, "-")
}

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
