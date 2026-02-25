package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// TokenBearerInitialUI represents an identity that authenticates using a token issued by LXD.
// This identity type is special in that there can only ever be one.
// Tokens issued for this identity are not valid when set in the Authorization header.
// Instead, they must be sent to LXDs root URL where they are set as a cookie.
// The cookie can be used to authenticate to the main API.
// Only this identity can authenticate via bearer token set as a cookie.
type TokenBearerInitialUI struct {
	typeInfoCommon
}

// Name returns the name of the TokenBearerInitialUI identity type.
func (TokenBearerInitialUI) Name() string {
	return api.IdentityTypeBearerTokenInitialUI
}

// Code returns the database code for TokenBearerInitialUI.
func (TokenBearerInitialUI) Code() int64 {
	return identityTypeBearerInitialUI
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerInitialUI) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsAdmin indicates that this identity has full access to LXD.
// This is required so that the user can explore LXD UI features and configure OIDC or TLS authentication.
func (TokenBearerInitialUI) IsAdmin() bool {
	return true
}
