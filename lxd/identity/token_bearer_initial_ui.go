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

// IsCacheable returns true to indicate that this identity type requires some data to be stored in the cache.
// In this case, the cache needs the identities' token secret.
func (TokenBearerInitialUI) IsCacheable() bool {
	return true
}

// TokenBearerInitialUIPending represents an initial UI token bearer identity for which no token is currently issued.
// An identity is pending before its first token is issued and again after its most recent token is revoked.
// It is neither an administrator nor cacheable, as a pending identity cannot authenticate.
// As with [TokenBearerInitialUI], there can only ever be one initial UI identity, whether active or pending.
type TokenBearerInitialUIPending struct {
	typeInfoCommon
}

// Name returns the name of the TokenBearerInitialUIPending identity type.
func (TokenBearerInitialUIPending) Name() string {
	return api.IdentityTypeBearerTokenInitialUIPending
}

// Code returns the database code for TokenBearerInitialUIPending.
func (TokenBearerInitialUIPending) Code() int64 {
	return identityTypeBearerInitialUIPending
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerInitialUIPending) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsPending indicates that this identity is pending.
func (TokenBearerInitialUIPending) IsPending() bool {
	return true
}
