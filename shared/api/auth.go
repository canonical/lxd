package api

const (
	// AuthenticationMethodTLS is the default authentication method for interacting with LXD remotely.
	AuthenticationMethodTLS = "tls"

	// AuthenticationMethodCandid is a macaroon based authentication method.
	AuthenticationMethodCandid = "candid"

	// AuthenticationMethodOIDC is a token based authentication method.
	AuthenticationMethodOIDC = "oidc"
)
