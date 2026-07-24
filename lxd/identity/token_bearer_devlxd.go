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

// IsCacheable returns true to indicate that this identity type requires some data to be stored in the cache.
// In this case, the cache needs the identities' token secret.
func (TokenBearerDevLXD) IsCacheable() bool {
	return true
}

// TokenBearerDevLXDPending represents a DevLXD token bearer identity for which no token is currently issued.
// An identity is pending before its first token is issued and again after its most recent token is revoked.
// It supports fine-grained permissions (so it can be added to groups while pending, allowing the eventual token
// holder to assume the correct permissions) but is not cacheable, as a pending identity cannot authenticate.
type TokenBearerDevLXDPending struct {
	typeInfoCommon
}

// Name returns the name of the TokenBearerDevLXDPending identity type.
func (TokenBearerDevLXDPending) Name() string {
	return api.IdentityTypeBearerTokenDevLXDPending
}

// Code returns the database code for TokenBearerDevLXDPending.
func (TokenBearerDevLXDPending) Code() int64 {
	return identityTypeBearerDevLXDPending
}

// AuthenticationMethod indicates that identities of this type authenticate via bearer token.
func (TokenBearerDevLXDPending) AuthenticationMethod() string {
	return api.AuthenticationMethodBearer
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (TokenBearerDevLXDPending) IsFineGrained() bool {
	return true
}

// IsPending indicates that this identity is pending.
func (TokenBearerDevLXDPending) IsPending() bool {
	return true
}
