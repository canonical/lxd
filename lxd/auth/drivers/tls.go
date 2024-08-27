//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
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
	identities *identity.Cache
}

func (t *tls) load(ctx context.Context, identityCache *identity.Cache, opts Opts) error {
	if identityCache == nil {
		return errors.New("TLS authorization driver requires an identity cache")
	}

	t.identities = identityCache
	return nil
}

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (t *tls) CheckPermission(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement) error {
	// Untrusted requests are denied.
	if !auth.IsTrusted(ctx) {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	isRoot, err := auth.IsServerAdmin(ctx, t.identities)
	if err != nil {
		return fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	if isRoot {
		return nil
	}

	id, err := auth.GetIdentityFromCtx(ctx, t.identities)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity: %w", err)
	}

	if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return nil
	}

	entityType, projectName, _, _, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Failed to parse entity URL: %w", err)
	}

	// Check server level object types
	switch entityType {
	case entity.TypeServer:
		if entitlement == auth.EntitlementCanView || entitlement == auth.EntitlementCanViewResources || entitlement == auth.EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	case entity.TypeStoragePool, entity.TypeCertificate:
		if entitlement == auth.EntitlementCanView {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Don't allow project modifications.
	if entityType == entity.TypeProject && (entitlement == auth.EntitlementCanEdit || entitlement == auth.EntitlementCanDelete) {
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	if !shared.ValueInSlice(projectName, id.Projects) {
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *tls) GetPermissionChecker(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	if !auth.IsTrusted(ctx) {
		return allowFunc(false), nil
	}

	isRoot, err := auth.IsServerAdmin(ctx, t.identities)
	if err != nil {
		return nil, fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	if isRoot {
		return allowFunc(true), nil
	}

	id, err := auth.GetIdentityFromCtx(ctx, t.identities)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller identity: %w", err)
	}

	if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return allowFunc(true), nil
	}

	// Check server level object types
	switch entityType {
	case entity.TypeServer:
		// We have to keep EntitlementCanViewMetrics here for backwards compatibility with older versions of LXD.
		// Historically when viewing the metrics endpoint for a specific project with a restricted certificate
		// also the internal server metrics get returned.
		if entitlement == auth.EntitlementCanView || entitlement == auth.EntitlementCanViewResources || entitlement == auth.EntitlementCanViewMetrics {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	case entity.TypeStoragePool, entity.TypeCertificate:
		if entitlement == auth.EntitlementCanView {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	}

	// Filter objects by project.
	return func(entityURL *api.URL) bool {
		eType, project, _, _, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			logger.Warn("Permission checker failed to parse entity URL", logger.Ctx{"entity_url": entityURL, "err": err})
			return false
		}

		// GetPermissionChecker can only be used to check permissions on entities of the same type, e.g. a list of instances.
		if eType != entityType {
			logger.Warn("Permission checker received URL with unexpected entity type", logger.Ctx{"expected": entityType, "actual": eType, "entity_url": entityURL})
			return false
		}

		// Otherwise, check if the project is in the list of allowed projects for the entity.
		return shared.ValueInSlice(project, id.Projects)
	}, nil
}
