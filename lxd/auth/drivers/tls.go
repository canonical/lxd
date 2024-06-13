//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
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
func (t *tls) CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement auth.Entitlement) error {
	details, err := t.requestDetails(r)
	if err != nil {
		return fmt.Errorf("Failed to extract request details: %w", err)
	}

	// Untrusted requests are denied.
	if !details.trusted {
		return api.StatusErrorf(http.StatusForbidden, http.StatusText(http.StatusForbidden))
	}

	if details.isInternalOrUnix() || details.isPKI {
		return nil
	}

	authenticationProtocol := details.authenticationProtocol()
	if authenticationProtocol != api.AuthenticationMethodTLS {
		t.logger.Warn("Authentication protocol is not compatible with authorization driver", logger.Ctx{"protocol": authenticationProtocol})
		// Return nil. If the server has been configured with an authentication method but no associated authorization driver,
		// the default is to give these authenticated users admin privileges.
		return nil
	}

	username := details.username()
	id, err := t.identities.Get(api.AuthenticationMethodTLS, details.username())
	if err != nil {
		return fmt.Errorf("Failed loading certificate for %q: %w", username, err)
	}

	isRestricted, err := identity.IsRestrictedIdentityType(id.IdentityType)
	if err != nil {
		return fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	if !isRestricted {
		return nil
	} else if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return nil
	}

	if details.isAllProjectsRequest {
		// Only admins (users with non-restricted certs) can use the all-projects parameter.
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	entityType, projectName, _, pathArgs, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Failed to parse entity URL: %w", err)
	}

	if entity.Equal(entity.TypeProject, entityType) {
		projectName = pathArgs[0]
	}

	// Check server level object types

	if entity.Equal(entity.TypeServer, entityType) {
		if entitlement == auth.EntitlementCanView || entitlement == auth.EntitlementCanViewResources || entitlement == auth.EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	} else if entity.Equal(entity.TypeStoragePool, entityType) || entity.Equal(entity.TypeCertificate, entityType) {
		if entitlement == auth.EntitlementCanView {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Don't allow project modifications.
	if entity.Equal(entity.TypeProject, entityType) && (entitlement == auth.EntitlementCanEdit || entitlement == auth.EntitlementCanDelete) {
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	if !shared.ValueInSlice(projectName, id.Projects) {
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *tls) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	details, err := t.requestDetails(r)
	if err != nil {
		return nil, fmt.Errorf("Failed to extract request details: %w", err)
	}

	// Untrusted requests are denied.
	if !details.trusted {
		return allowFunc(false), nil
	}

	if details.isInternalOrUnix() || details.isPKI {
		return allowFunc(true), nil
	}

	authenticationProtocol := details.authenticationProtocol()
	if authenticationProtocol != api.AuthenticationMethodTLS {
		t.logger.Warn("Authentication protocol is not compatible with authorization driver", logger.Ctx{"protocol": authenticationProtocol})
		// Allow all. If the server has been configured with an authentication method but no associated authorization driver,
		// the default is to give these authenticated users admin privileges.
		return allowFunc(true), nil
	}

	username := details.username()
	id, err := t.identities.Get(api.AuthenticationMethodTLS, details.username())
	if err != nil {
		return nil, fmt.Errorf("Failed loading certificate for %q: %w", username, err)
	}

	isRestricted, err := identity.IsRestrictedIdentityType(id.IdentityType)
	if err != nil {
		return nil, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	if !isRestricted {
		return allowFunc(true), nil
	} else if id.IdentityType == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return allowFunc(true), nil
	}

	if details.isAllProjectsRequest {
		// Only admins (users with non-restricted certs) can use the all-projects parameter.
		return nil, api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check server level object types
	if entity.Equal(entity.TypeServer, entityType) {
		// We have to keep EntitlementCanViewMetrics here for backwards compatibility with older versions of LXD.
		// Historically when viewing the metrics endpoint for a specific project with a restricted certificate
		// also the internal server metrics get returned.
		if entitlement == auth.EntitlementCanView || entitlement == auth.EntitlementCanViewResources || entitlement == auth.EntitlementCanViewMetrics {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	} else if entity.Equal(entity.TypeStoragePool, entityType) || entity.Equal(entity.TypeCertificate, entityType) {
		if entitlement == auth.EntitlementCanView {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	}

	effectiveProject, _ := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)

	// Filter objects by project.
	return func(entityURL *api.URL) bool {
		eType, project, _, pathArgs, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			logger.Warn("Permission checker failed to parse entity URL", logger.Ctx{"entity_url": entityURL, "error": err})
			return false
		}

		// GetPermissionChecker can only be used to check permissions on entities of the same type, e.g. a list of instances.
		if eType != entityType {
			logger.Warn("Permission checker received URL with unexpected entity type", logger.Ctx{"expected": entityType, "actual": eType, "entity_url": entityURL})
			return false
		}

		// If it's a project URL, the project name is in the path, not the query parameter.
		if entity.Equal(entity.TypeProject, eType) {
			project = pathArgs[0]
		}

		// If an effective project has been set in the request context. We expect all entities to be in that project.
		if effectiveProject != "" {
			return project == effectiveProject
		}

		// Otherwise, check if the project is in the list of allowed projects for the entity.
		return shared.ValueInSlice(project, id.Projects)
	}, nil
}
