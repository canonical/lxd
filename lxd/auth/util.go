package auth

import (
	"net/http"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// IsDeniedError returns true if the error is not found or forbidden. This is because the CheckPermission method on
// Authorizer will return a not found error if the requestor does not have access to view the resource. If a requestor
// has view access, but not edit access a forbidden error is returned.
func IsDeniedError(err error) bool {
	return api.StatusErrorCheck(err, http.StatusNotFound, http.StatusForbidden)
}

// ValidateAuthenticationMethod returns an api.StatusError with http.StatusBadRequest if the given authentication
// method is not recognised.
func ValidateAuthenticationMethod(authenticationMethod string) error {
	if !shared.ValueInSlice(authenticationMethod, []string{api.AuthenticationMethodTLS, api.AuthenticationMethodOIDC}) {
		return api.StatusErrorf(http.StatusBadRequest, "Unrecognized authentication method %q", authenticationMethod)
	}

	return nil
}
