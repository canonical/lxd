package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// TokenBearerClient represents an identity that authenticates using a token issued by LXD
// and whose permissions are managed via group membership. The token is valid for the LXD API.
// It supports both caching and fine-grained permissions but is not an admin by default.
type TokenBearerClient struct {
	typeInfoCommon
}

// Name returns the name of the BearerToken identity type.
func (TokenBearerClient) Name() string {
	return api.IdentityTypeBearerTokenClient
}

// Code returns the database code for BearerToken.
func (TokenBearerClient) Code() int64 {
	return identityTypeBearerClient
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerClient) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (TokenBearerClient) IsFineGrained() bool {
	return true
}
