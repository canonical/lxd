package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// DevLXDTokenBearer represents an identity that authenticates using a token issued by LXD
// and whose permissions are managed via group membership. The token is only valid for the DevLXD API.
// It supports both caching and fine-grained permissions but is not an admin by default.
type DevLXDTokenBearer struct {
	typeInfoCommon
}

// Name returns the name of the DevLXDTokenBearer identity type.
func (DevLXDTokenBearer) Name() string {
	return api.IdentityTypeBearerTokenDevLXD
}

// Code returns the database code for DevLXDTokenBearer.
func (DevLXDTokenBearer) Code() int64 {
	return identityTypeBearerDevLXD
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (DevLXDTokenBearer) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (DevLXDTokenBearer) IsFineGrained() bool {
	return true
}
