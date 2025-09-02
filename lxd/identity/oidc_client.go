package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// OIDCClient represents an identity that authenticates using OpenID Connect (OIDC).
// It supports caching and fine-grained permissions but is not an admin by default.
type OIDCClient struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that OIDC clients authenticate using OIDC.
func (OIDCClient) AuthenticationMethod() string {
	return api.AuthenticationMethodOIDC
}

// Code returns the identity type code for this identity type.
func (OIDCClient) Code() int64 {
	return identityTypeOIDCClient
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (OIDCClient) IsFineGrained() bool {
	return true
}

// Name returns the API name of this identity type.
func (OIDCClient) Name() string {
	return api.IdentityTypeOIDCClient
}
