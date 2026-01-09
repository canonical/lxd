package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// LXDTokenBearer represents an identity that authenticates using a token issued by LXD
// and whose permissions are managed via group membership. The token is valid for the LXD API.
// It supports both caching and fine-grained permissions but is not an admin by default.
type LXDTokenBearer struct {
	typeInfoCommon
}

// Name returns the name of the BearerToken identity type.
func (LXDTokenBearer) Name() string {
	return api.IdentityTypeBearerTokenLXD
}

// Code returns the database code for BearerToken.
func (LXDTokenBearer) Code() int64 {
	return identityTypeBearerLXD
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (LXDTokenBearer) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (LXDTokenBearer) IsFineGrained() bool {
	return true
}
