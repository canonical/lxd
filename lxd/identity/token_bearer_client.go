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

// IsCacheable returns true to indicate that this identity type requires some data to be stored in the cache.
// In this case, the cache needs the identities' token secret.
func (TokenBearerClient) IsCacheable() bool {
	return true
}

// TokenBearerClientPending represents a client token bearer identity for which no token is currently issued.
// An identity is pending before its first token is issued and again after its most recent token is revoked.
// It supports fine-grained permissions (so it can be added to groups while pending, allowing the eventual token
// holder to assume the correct permissions) but is not cacheable, as a pending identity cannot authenticate.
type TokenBearerClientPending struct {
	typeInfoCommon
}

// Name returns the name of the TokenBearerClientPending identity type.
func (TokenBearerClientPending) Name() string {
	return api.IdentityTypeBearerTokenClientPending
}

// Code returns the database code for TokenBearerClientPending.
func (TokenBearerClientPending) Code() int64 {
	return identityTypeBearerClientPending
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerClientPending) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (TokenBearerClientPending) IsFineGrained() bool {
	return true
}

// IsPending indicates that this identity is pending.
func (TokenBearerClientPending) IsPending() bool {
	return true
}
