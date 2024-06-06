package auth

import (
	"net/http"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// ValidateAuthenticationMethod returns an api.StatusError with http.StatusBadRequest if the given authentication
// method is not recognised.
func ValidateAuthenticationMethod(authenticationMethod string) error {
	if !shared.ValueInSlice(authenticationMethod, []string{api.AuthenticationMethodTLS, api.AuthenticationMethodOIDC}) {
		return api.StatusErrorf(http.StatusBadRequest, "Unrecognized authentication method %q", authenticationMethod)
	}

	return nil
}
