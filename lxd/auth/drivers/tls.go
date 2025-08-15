//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
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

func (t *tls) load(ctx context.Context, identityCache *identity.Cache, opts Opts) error {
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

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return err
	}

	// Untrusted requests are denied.
	if !requestor.IsTrusted() {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	// Cluster or unix socket requests have admin permission.
	if requestor.IsAdmin() {
		return nil
	}

	id := requestor.CallerIdentity()
	if id == nil {
		return errors.New("No identity is set in the request details")
	}

	if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return nil
	}

	projectSpecific, err := entityType.RequiresProject()
	if err != nil {
		return fmt.Errorf("Failed to check project specificity of entity type %q: %w", entityType, err)
	}

	// Check non- project-specific entity types.
	if !projectSpecific {
		if t.allowProjectUnspecificEntityType(entitlement, entityType, id, projectName, pathArguments) {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	if !slices.Contains(id.Projects, projectName) {
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// CheckPermissionWithoutEffectiveProject calls CheckPermission. This is because the TLS auth driver does not need to consider
// the effective project at all.
func (t *tls) CheckPermissionWithoutEffectiveProject(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement) error {
	return t.CheckPermission(ctx, entityURL, entitlement)
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

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return nil, err
	}

	// Untrusted requests are denied.
	if !requestor.IsTrusted() {
		return allowFunc(false), nil
	}

	// Cluster or unix socket requests have admin permission.
	if requestor.IsAdmin() {
		return allowFunc(true), nil
	}

	id := requestor.CallerIdentity()
	if id == nil {
		return nil, errors.New("No identity is set in the request details")
	}

	if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return allowFunc(true), nil
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
			return t.allowProjectUnspecificEntityType(entitlement, entityType, id, project, pathArguments)
		}

		// Otherwise, check if the project is in the list of allowed projects for the entity.
		return slices.Contains(id.Projects, project)
	}, nil
}

// GetPermissionCheckerWithoutEffectiveProject calls GetPermissionChecker. This is because the TLS auth driver does not need to consider
// the effective project at all.
func (t *tls) GetPermissionCheckerWithoutEffectiveProject(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	return t.GetPermissionChecker(ctx, entitlement, entityType)
}

func (t *tls) allowProjectUnspecificEntityType(entitlement auth.Entitlement, entityType entity.Type, id *identity.CacheEntry, projectName string, pathArguments []string) bool {
	switch entityType {
	case entity.TypeServer:
		// Restricted TLS certificates have the following entitlements on server.
		//
		// Note: We have to keep EntitlementCanViewMetrics here for backwards compatibility with older versions of LXD.
		// Historically when viewing the metrics endpoint for a specific project with a restricted certificate also the
		// internal server metrics get returned.
		return slices.Contains([]auth.Entitlement{auth.EntitlementCanViewResources, auth.EntitlementCanViewMetrics, auth.EntitlementCanViewUnmanagedNetworks}, entitlement)
	case entity.TypeIdentity:
		// If the entity URL refers to the identity that made the request, then the second path argument of the URL is
		// the identifier of the identity. This line allows the caller to view their own identity and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 1 && pathArguments[1] == id.Identifier
	case entity.TypeCertificate:
		// If the certificate URL refers to the identity that made the request, then the first path argument of the URL is
		// the identifier of the identity (their fingerprint). This line allows the caller to view their own certificate and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 0 && pathArguments[0] == id.Identifier
	case entity.TypeProject:
		// If the project is in the list of projects that the identity is restricted to, then they have the following
		// entitlements.
		return slices.Contains(id.Projects, projectName) && slices.Contains([]auth.Entitlement{auth.EntitlementCanView, auth.EntitlementCanCreateImages, auth.EntitlementCanCreateImageAliases, auth.EntitlementCanCreateInstances, auth.EntitlementCanCreateNetworks, auth.EntitlementCanCreateNetworkACLs, auth.EntitlementCanCreateNetworkZones, auth.EntitlementCanCreateProfiles, auth.EntitlementCanCreateStorageVolumes, auth.EntitlementCanCreateStorageBuckets, auth.EntitlementCanViewEvents, auth.EntitlementCanViewOperations, auth.EntitlementCanViewMetrics}, entitlement)

	default:
		return false
	}
}
