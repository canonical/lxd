package entity

import (
	"context"
	"net/http"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// PermissionChecker is a type alias for a function that returns whether a user has required permissions on an object.
// It is returned by Authorizer.GetPermissionChecker.
type PermissionChecker func(entityURL *api.URL) bool

// Authorizer is the primary external API for this package.
type Authorizer interface {
	Driver() string

	CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement Entitlement) error
	GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, entityType entity.Type) (PermissionChecker, error)
}

// IsDeniedError returns true if the error is not found or forbidden. This is because the CheckPermission method on
// Authorizer will return a not found error if the requestor does not have access to view the resource. If a requestor
// has view access, but not edit access a forbidden error is returned.
func IsDeniedError(err error) bool {
	return api.StatusErrorCheck(err, http.StatusNotFound, http.StatusForbidden)
}
