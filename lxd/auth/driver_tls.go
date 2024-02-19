package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

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
func (t *tls) CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement Entitlement) error {
	details, err := t.requestDetails(r)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
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
	} else if id.IdentityType == api.IdentityTypeCertificateMetrics && entitlement == EntitlementCanViewMetrics {
		return nil
	}

	if details.isAllProjectsRequest {
		// Only admins (users with non-restricted certs) can use the all-projects parameter.
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	entityType, projectName, _, _, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Failed to parse entity URL: %w", err)
	}

	// Check server level object types
	switch entityType {
	case entity.TypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	case entity.TypeStoragePool, entity.TypeCertificate:
		if entitlement == EntitlementCanView {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	if !shared.ValueInSlice(projectName, id.Projects) {
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *tls) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, entityType entity.Type) (PermissionChecker, error) {
	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	details, err := t.requestDetails(r)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
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
	} else if id.IdentityType == api.IdentityTypeCertificateMetrics && entitlement == EntitlementCanViewMetrics {
		return allowFunc(true), nil
	}

	if details.isAllProjectsRequest {
		// Only admins (users with non-restricted certs) can use the all-projects parameter.
		return nil, api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check server level object types
	switch entityType {
	case entity.TypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	case entity.TypeStoragePool, entity.TypeCertificate:
		if entitlement == EntitlementCanView {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	}

	// Error if user does not have access to the project (unless we're getting projects, where we want to filter the results).
	if !shared.ValueInSlice(details.projectName, id.Projects) && entityType != entity.TypeProject {
		return nil, api.StatusErrorf(http.StatusForbidden, "User does not have permissions for project %q", details.projectName)
	}

	// Filter objects by project.
	return func(entityURL *api.URL) bool {
		eType, project, _, pathArgs, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			logger.Warn("Permission checker failed to parse entity URL", logger.Ctx{"entity_url": entityURL, "error": err})
			return false
		}

		if eType == entity.TypeProject {
			project = pathArgs[0]
		}

		if eType != entityType {
			logger.Warn("Permission checker received URL with unexpected entity type", logger.Ctx{"expected": entityType, "actual": eType, "entity_url": entityURL})
			return false
		}

		return shared.ValueInSlice(project, id.Projects)
	}, nil
}
