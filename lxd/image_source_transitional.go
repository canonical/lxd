package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/registry"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// resolveDeprecatedInstanceSource translates the deprecated Server and Protocol fields of an
// image instance source into an image registry, setting source.ImageRegistry so the rest of
// the request is handled as if the registry had been specified. It is a no-op unless the source
// is an image source that uses the deprecated fields without an explicit image registry.
func resolveDeprecatedInstanceSource(ctx context.Context, s *state.State, projectName string, source *api.InstanceSource) error {
	server := source.Server           //nolint:staticcheck // Deprecated field read for backward compatibility.
	protocol := source.Protocol       //nolint:staticcheck // Deprecated field read for backward compatibility.
	certificate := source.Certificate //nolint:staticcheck // Deprecated field read for backward compatibility.

	// Only image sources that use the deprecated fields without an explicit registry are translated.
	if source.Type != api.SourceTypeImage || source.ImageRegistry != "" || server == "" {
		return nil
	}

	registryName, err := resolveDeprecatedImageRegistry(ctx, s, projectName, server, protocol, certificate)
	if err != nil {
		return err
	}

	source.ImageRegistry = registryName

	return nil
}

// resolveDeprecatedImagesPostSource translates the deprecated Server and Protocol fields of a
// remote image source into an image registry, setting source.ImageRegistry so the rest of the
// request is handled as if the registry had been specified. It is a no-op unless the source is a
// remote image source that uses the deprecated fields without an explicit image registry.
func resolveDeprecatedImagesPostSource(ctx context.Context, s *state.State, projectName string, source *api.ImagesPostSource) error {
	server := source.Server           //nolint:staticcheck // Deprecated field read for backward compatibility.
	protocol := source.Protocol       //nolint:staticcheck // Deprecated field read for backward compatibility.
	certificate := source.Certificate //nolint:staticcheck // Deprecated field read for backward compatibility.

	// Only remote image sources that use the deprecated fields without an explicit registry are translated.
	if source.Type != api.SourceTypeImage || source.ImageRegistry != "" || server == "" {
		return nil
	}

	registryName, err := resolveDeprecatedImageRegistry(ctx, s, projectName, server, protocol, certificate)
	if err != nil {
		return err
	}

	source.ImageRegistry = registryName

	return nil
}

// resolveDeprecatedImageRegistry resolves the deprecated server and protocol of an image source to
// an image registry name. For the LXD protocol it matches an existing image registry whose cluster
// link reaches the requested server, preferring a match on the source certificate fingerprint and
// falling back to the server address. For the SimpleStreams protocol it matches an existing image
// registry by URL and, failing that, auto-creates a public SimpleStreams registry when the URL is
// on the transitional allowlist and the caller is permitted to create image registries.
func resolveDeprecatedImageRegistry(ctx context.Context, s *state.State, projectName string, server string, protocol string, certificate string) (string, error) {
	if protocol == "" {
		return "", api.StatusErrorf(http.StatusBadRequest, "Missing image source protocol; a protocol must be specified")
	}

	var registryName string
	var created bool
	var err error
	switch protocol {
	case api.ImageRegistryProtocolLXD:
		registryName, err = resolveDeprecatedLXDServer(ctx, s, server, certificate)
	case api.ImageRegistryProtocolSimpleStreams:
		registryName, created, err = resolveDeprecatedSimpleStreamsServer(ctx, s, projectName, server)
	default:
		return "", api.StatusErrorf(http.StatusBadRequest, "Unsupported image source protocol %q", protocol)
	}

	if err != nil {
		return "", err
	}

	if created {
		logger.Warn("Image source used deprecated server and protocol fields; auto-created an image registry", logger.Ctx{"server": server, "protocol": protocol, "image_registry": registryName})
	} else {
		logger.Warn("Image source used deprecated server and protocol fields; mapped to an image registry", logger.Ctx{"server": server, "protocol": protocol, "image_registry": registryName})
	}

	return registryName, nil
}

// resolveDeprecatedLXDServer returns the name of the LXD image registry that corresponds to the
// requested server, preferring a match on the source certificate fingerprint and falling back to
// the server address. Certificate matching is preferred because it is robust to differences in how
// the same server is addressed (for example a hostname versus an IP). It errors when no registry
// matches.
func resolveDeprecatedLXDServer(ctx context.Context, s *state.State, server string, certificate string) (string, error) {
	serverAddress, err := hostPortFromServerURL(server)
	if err != nil {
		return "", err
	}

	// Compute the fingerprint of the source certificate, if one was provided.
	var sourceFingerprint string
	if certificate != "" {
		sourceFingerprint, err = shared.CertFingerprintStr(certificate)
		if err != nil {
			return "", api.StatusErrorf(http.StatusBadRequest, "Invalid image source certificate: %v", err)
		}
	}

	var matchedRegistry string
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		registries, err := dbCluster.GetImageRegistries(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading image registries: %w", err)
		}

		registryConfigs, err := dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading image registry config: %w", err)
		}

		links, err := dbCluster.GetClusterLinks(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading cluster links: %w", err)
		}

		linkConfigs, err := dbCluster.ClusterLinksConfigStore().GetAll(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading cluster link config: %w", err)
		}

		// Only load cluster link certificate fingerprints when a source certificate was provided.
		var linkFingerprints map[int64]string
		if sourceFingerprint != "" {
			linkFingerprints, err = dbCluster.GetClusterLinkCertificateFingerprints(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading cluster link certificates: %w", err)
			}
		}

		// Build maps of cluster link name to its ID and to its known addresses.
		linkIDs := make(map[string]int64, len(links))
		linkAddresses := make(map[string][]string, len(links))
		for _, link := range links {
			linkIDs[link.Name] = link.ID
			linkAddresses[link.Name] = shared.SplitNTrimSpace(linkConfigs[link.ID]["volatile.addresses"], ",", -1, false)
		}

		matchedRegistry = matchDeprecatedLXDRegistry(registries, registryConfigs, linkIDs, linkFingerprints, linkAddresses, sourceFingerprint, serverAddress)

		return nil
	})
	if err != nil {
		return "", err
	}

	if matchedRegistry == "" {
		return "", api.StatusErrorf(http.StatusBadRequest, "No image registry found for LXD server %q; create an image registry backed by a cluster link to this server", server)
	}

	return matchedRegistry, nil
}

// matchDeprecatedLXDRegistry selects the LXD image registry that corresponds to a deprecated LXD
// image source. It prefers a match on the source certificate fingerprint (comparing against each
// registry's backing cluster link certificate) and falls back to a match on the server address. It
// returns an empty string when no registry matches.
func matchDeprecatedLXDRegistry(registries []dbCluster.ImageRegistriesRow, registryConfigs map[int64]map[string]string, linkIDs map[string]int64, linkFingerprints map[int64]string, linkAddresses map[string][]string, sourceFingerprint string, serverAddress string) string {
	var addressMatch string
	for _, dbRegistry := range registries {
		if string(dbRegistry.Protocol) != api.ImageRegistryProtocolLXD {
			continue
		}

		linkName := registryConfigs[dbRegistry.ID]["cluster"]
		if linkName == "" {
			continue
		}

		// Prefer a certificate fingerprint match, which is address-independent.
		if sourceFingerprint != "" && linkFingerprints[linkIDs[linkName]] == sourceFingerprint {
			return dbRegistry.Name
		}

		// Remember the first address match, but keep scanning in case a later registry matches by
		// certificate fingerprint.
		if addressMatch == "" && slices.Contains(linkAddresses[linkName], serverAddress) {
			addressMatch = dbRegistry.Name
		}
	}

	return addressMatch
}

// resolveDeprecatedSimpleStreamsServer returns the name of the SimpleStreams image registry matching
// the requested server URL, auto-creating one when the URL is on the transitional allowlist. The
// returned boolean reports whether a registry was created.
func resolveDeprecatedSimpleStreamsServer(ctx context.Context, s *state.State, projectName string, server string) (string, bool, error) {
	normalizedURL := normalizeSimpleStreamsURL(server)

	var matchedRegistry string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		registries, err := dbCluster.GetImageRegistries(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading image registries: %w", err)
		}

		registryConfigs, err := dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading image registry config: %w", err)
		}

		for _, dbRegistry := range registries {
			if string(dbRegistry.Protocol) != api.ImageRegistryProtocolSimpleStreams {
				continue
			}

			if normalizeSimpleStreamsURL(registryConfigs[dbRegistry.ID]["url"]) == normalizedURL {
				matchedRegistry = dbRegistry.Name
				break
			}
		}

		return nil
	})
	if err != nil {
		return "", false, err
	}

	if matchedRegistry != "" {
		return matchedRegistry, false, nil
	}

	// No existing registry matched. Only allowlisted SimpleStreams URLs may be auto-created.
	if !registry.IsTransitionalSimpleStreamsURL(normalizedURL) {
		return "", false, api.StatusErrorf(http.StatusBadRequest, "No image registry found for image source server %q", server)
	}

	registryName := registry.TransitionalRegistryName(normalizedURL)
	if registryName == "" {
		return "", false, api.StatusErrorf(http.StatusBadRequest, "Cannot derive an image registry name from image source server %q", server)
	}

	// Load the project config to evaluate the restricted.registries setting before creating.
	var projectConfig map[string]string
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projectConfig, err = dbCluster.GetProjectConfig(ctx, tx.Tx(), projectName)
		return err
	})
	if err != nil {
		return "", false, fmt.Errorf("Failed loading config for project %q: %w", projectName, err)
	}

	// Abide by the project's restricted.registries setting even in the transitional path. A registry
	// auto-created here is never built-in, so the built-in exemption does not apply.
	if !project.RegistryAllowed(projectConfig, registryName, false) {
		return "", false, api.StatusErrorf(http.StatusNotFound, "Image registry not found")
	}

	// Auto-creating an image registry requires permission to create image registries.
	err = s.Authorizer.CheckPermission(ctx, entity.ServerURL(), auth.EntitlementCanCreateImageRegistries)
	if err != nil {
		if auth.IsDeniedError(err) {
			return "", false, api.StatusErrorf(http.StatusForbidden, "No image registry exists for image source server %q; an administrator must create the image registry", server)
		}

		return "", false, err
	}

	err = createTransitionalSimpleStreamsRegistry(ctx, s, registryName, normalizedURL)
	if err != nil {
		return "", false, err
	}

	return registryName, true, nil
}

// createTransitionalSimpleStreamsRegistry creates a public SimpleStreams image registry for the
// given URL. It uses create-or-get semantics: if a registry with the same name already exists
// (for example created by a concurrent request), it is treated as success.
func createTransitionalSimpleStreamsRegistry(ctx context.Context, s *state.State, name string, registryURL string) error {
	requestor := request.CreateRequestor(ctx)

	created := false
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err == nil {
			// Registry already exists, use it.
			return nil
		}

		if !response.IsNotFoundError(err) {
			return err
		}

		registryID, err := dbCluster.CreateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistriesRow{
			Name:     name,
			Protocol: dbCluster.ImageRegistryProtocol(api.ImageRegistryProtocolSimpleStreams),
			Builtin:  false,
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				// Created concurrently, use it.
				return nil
			}

			return fmt.Errorf("Failed adding image registry database record: %w", err)
		}

		err = dbCluster.CreateImageRegistryConfig(ctx, tx.Tx(), registryID, map[string]string{"url": registryURL})
		if err != nil {
			return fmt.Errorf("Failed adding image registry config database record: %w", err)
		}

		created = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed creating image registry %q: %w", name, err)
	}

	if created {
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryCreated.Event(name, requestor, nil))
	}

	return nil
}

// hostPortFromServerURL extracts the host and port from a deprecated LXD image source server URL.
func hostPortFromServerURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err == nil && u.Host != "" {
		return u.Host, nil
	}

	// The server may be a bare host:port without a scheme, which does not parse as a URL.
	u, err = url.Parse("https://" + server)
	if err != nil {
		return "", api.StatusErrorf(http.StatusBadRequest, "Invalid image source server %q: %v", server, err)
	}

	return u.Host, nil
}

// normalizeSimpleStreamsURL canonicalizes a SimpleStreams URL for comparison: it lowercases the
// scheme and host, drops the scheme's default port, preserves the path, and trims a single
// trailing slash. Dropping the default port lets URLs that differ only by an explicit default
// port compare equal; for example the Terraform provider appends ":443" to a port-less
// SimpleStreams remote address, whereas a registry URL created via the CLI usually omits it.
func normalizeSimpleStreamsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSuffix(raw, "/")
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Drop the scheme's default port so that, for example, "https://host:443" and "https://host"
	// compare equal.
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			// IPv6 literals must remain bracketed.
			u.Host = "[" + host + "]"
		} else {
			u.Host = host
		}
	}

	return strings.TrimSuffix(u.String(), "/")
}
