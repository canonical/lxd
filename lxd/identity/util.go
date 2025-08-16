package identity

import (
	"net/http"
	"slices"

	"github.com/canonical/lxd/shared/api"
)

// ValidateAuthenticationMethod returns an api.StatusError with http.StatusBadRequest if the given authentication
// method is not recognised.
func ValidateAuthenticationMethod(authenticationMethod string) error {
	if !slices.Contains([]string{api.AuthenticationMethodTLS, api.AuthenticationMethodOIDC, api.AuthenticationMethodBearer}, authenticationMethod) {
		return api.StatusErrorf(http.StatusBadRequest, "Unrecognized authentication method %q", authenticationMethod)
	}

	return nil
}
