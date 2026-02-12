package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// TokenBearerDevLXD represents an identity that authenticates using a token issued by LXD
// and whose permissions are managed via group membership. The token is only valid for the DevLXD API.
// It supports both caching and fine-grained permissions but is not an admin by default.
type TokenBearerDevLXD struct {
	typeInfoCommon
}

// Name returns the name of the DevLXDTokenBearer identity type.
func (TokenBearerDevLXD) Name() string {
	return api.IdentityTypeBearerTokenDevLXD
}

// Code returns the database code for DevLXDTokenBearer.
func (TokenBearerDevLXD) Code() int64 {
	return identityTypeBearerDevLXD
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerDevLXD) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (TokenBearerDevLXD) IsFineGrained() bool {
	return true
}
