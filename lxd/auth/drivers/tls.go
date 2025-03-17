//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

const (
	// DriverTLS is used at start up to allow communication between cluster members and initialise the cluster database.
	DriverTLS string = "tls"
)

func init() {
	authorizers[DriverTLS] = func() authorizer { return &tls{} }
}

type tls struct {
	commonAuthorizer
}

func (t *tls) load(ctx context.Context, opts Opts) error {
	return nil
}

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (t *tls) CheckPermission(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement) error {
	entityType, projectName, _, pathArguments, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Failed to parse entity URL: %w", err)
	}

	err = auth.ValidateEntitlement(entityType, entitlement)
	if err != nil {
		return fmt.Errorf("Cannot check permissions for entity type %q and entitlement %q: %w", entityType, entitlement, err)
	}

	// Untrusted requests are denied.
	if !auth.IsTrusted(ctx) {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	isRoot, err := auth.IsServerAdmin(ctx)
	if err != nil {
		return fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	if isRoot {
		return nil
	}

	id, err := auth.GetIdentityFromCtx(ctx)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity: %w", err)
	}

	if id.Type == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return nil
	}

	projectSpecific, err := entityType.RequiresProject()
	if err != nil {
		return fmt.Errorf("Failed to check project specificity of entity type %q: %w", entityType, err)
	}

	cert, err := auth.GetCertificateFromCtx(ctx)
	if err != nil {
		return fmt.Errorf("Failed to get caller certificate: %w", err)
	}

	// Check non- project-specific entity types.
	if !projectSpecific {
		if t.allowProjectUnspecificEntityType(entitlement, entityType, cert, projectName, pathArguments) {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	if !shared.ValueInSlice(projectName, cert.Projects) {
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *tls) GetPermissionChecker(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	err := auth.ValidateEntitlement(entityType, entitlement)
	if err != nil {
		return nil, fmt.Errorf("Cannot get a permission checker for entity type %q and entitlement %q: %w", entityType, entitlement, err)
	}

	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	if !auth.IsTrusted(ctx) {
		return allowFunc(false), nil
	}

	isRoot, err := auth.IsServerAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	if isRoot {
		return allowFunc(true), nil
	}

	id, err := auth.GetIdentityFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller identity: %w", err)
	}

	if id.Type == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return allowFunc(true), nil
	}

	cert, err := auth.GetCertificateFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller certificate: %w", err)
	}

	projectSpecific, err := entityType.RequiresProject()
	if err != nil {
		return nil, fmt.Errorf("Failed to check project specificity of entity type %q: %w", entityType, err)
	}

	// Filter objects by project.
	return func(entityURL *api.URL) bool {
		eType, project, _, pathArguments, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			logger.Warn("Permission checker failed to parse entity URL", logger.Ctx{"entity_url": entityURL, "err": err})
			return false
		}

		// GetPermissionChecker can only be used to check permissions on entities of the same type, e.g. a list of instances.
		if eType != entityType {
			logger.Warn("Permission checker received URL with unexpected entity type", logger.Ctx{"expected": entityType, "actual": eType, "entity_url": entityURL})
			return false
		}

		// Check non- project-specific entity types.
		if !projectSpecific {
			return t.allowProjectUnspecificEntityType(entitlement, entityType, cert, project, pathArguments)
		}

		// Otherwise, check if the project is in the list of allowed projects for the entity.
		return shared.ValueInSlice(project, cert.Projects)
	}, nil
}

func (t *tls) allowProjectUnspecificEntityType(entitlement auth.Entitlement, entityType entity.Type, cert *api.Certificate, projectName string, pathArguments []string) bool {
	switch entityType {
	case entity.TypeServer:
		// Restricted TLS certificates have the following entitlements on server.
		return shared.ValueInSlice(entitlement, []auth.Entitlement{auth.EntitlementCanViewResources, auth.EntitlementCanViewMetrics, auth.EntitlementCanViewUnmanagedNetworks})
	case entity.TypeIdentity:
		// If the entity URL refers to the identity that made the request, then the second path argument of the URL is
		// the identifier of the identity. This line allows the caller to view their own identity and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 1 && pathArguments[1] == cert.Fingerprint
	case entity.TypeCertificate:
		// If the certificate URL refers to the identity that made the request, then the first path argument of the URL is
		// the identifier of the identity (their fingerprint). This line allows the caller to view their own certificate and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 0 && pathArguments[0] == cert.Fingerprint
	case entity.TypeProject:
		// If the project is in the list of projects that the identity is restricted to, then they have the following
		// entitlements.
		return shared.ValueInSlice(projectName, cert.Projects) && shared.ValueInSlice(entitlement, []auth.Entitlement{
			auth.EntitlementCanView,
			auth.EntitlementCanCreateImages,
			auth.EntitlementCanCreateImageAliases,
			auth.EntitlementCanCreateInstances,
			auth.EntitlementCanCreateNetworks,
			auth.EntitlementCanCreateNetworkACLs,
			auth.EntitlementCanCreateNetworkZones,
			auth.EntitlementCanCreateProfiles,
			auth.EntitlementCanCreateStorageVolumes,
			auth.EntitlementCanCreateStorageBuckets,
			auth.EntitlementCanViewEvents,
			auth.EntitlementCanViewOperations,
			auth.EntitlementCanViewMetrics,
		})
	default:
		return false
	}
}
