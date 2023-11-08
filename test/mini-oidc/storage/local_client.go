package storage

import (
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// LXDDeviceClient creates a device client suitable for LXD.
func LXDDeviceClient(id string) *Client {
	return &Client{
		id:              id,
		redirectURIs:    nil,
		applicationType: op.ApplicationTypeNative,
		authMethod:      oidc.AuthMethodNone,
		responseTypes:   []oidc.ResponseType{oidc.ResponseTypeCode},
		grantTypes:      []oidc.GrantType{oidc.GrantTypeDeviceCode, oidc.GrantTypeRefreshToken},
		accessTokenType: op.AccessTokenTypeJWT,
	}
}
