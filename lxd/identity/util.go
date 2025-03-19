package identity

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/shared/api"
)

// IsFineGrainedIdentityType returns true if permissions of the identity type are managed via group membership.
func IsFineGrainedIdentityType(identityType string) bool {
	return slices.Contains([]string{api.IdentityTypeOIDCClient, api.IdentityTypeCertificateClient, api.IdentityTypeCertificateClientPending, api.IdentityTypeCertificateClusterLink, api.IdentityTypeCertificateClusterLinkPending}, identityType)
}

// IsRestrictedIdentityType returns whether the given identity is restricted or not. Identity types that are not
// restricted have full access to LXD. An error is returned if the identity type is not recognised.
func IsRestrictedIdentityType(identityType string) (bool, error) {
	_, err := AuthenticationMethodFromIdentityType(identityType)
	if err != nil {
		return false, err
	}

	return !slices.Contains([]string{api.IdentityTypeCertificateClientUnrestricted, api.IdentityTypeCertificateServer}, identityType), nil
}

// AuthenticationMethodFromIdentityType returns the authentication method corresponding to the given identity type. All
// identity types must correspond to an authentication method. An error is returned if the identity type is not recognised.
func AuthenticationMethodFromIdentityType(identityType string) (string, error) {
	switch identityType {
	case api.IdentityTypeCertificateClientRestricted, api.IdentityTypeCertificateClientUnrestricted, api.IdentityTypeCertificateServer, api.IdentityTypeCertificateMetricsRestricted, api.IdentityTypeCertificateMetricsUnrestricted, api.IdentityTypeCertificateClient:
		return api.AuthenticationMethodTLS, nil
	case api.IdentityTypeOIDCClient:
		return api.AuthenticationMethodOIDC, nil
	}

	return "", fmt.Errorf("Identity type %q not recognized", identityType)
}

// ValidateAuthenticationMethod returns an api.StatusError with http.StatusBadRequest if the given authentication
// method is not recognised.
func ValidateAuthenticationMethod(authenticationMethod string) error {
	if !slices.Contains([]string{api.AuthenticationMethodTLS, api.AuthenticationMethodOIDC}, authenticationMethod) {
		return api.StatusErrorf(http.StatusBadRequest, "Unrecognized authentication method %q", authenticationMethod)
	}

	return nil
}
