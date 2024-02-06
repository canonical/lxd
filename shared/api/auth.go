package api

const (
	// AuthenticationMethodTLS is the default authentication method for interacting with LXD remotely.
	AuthenticationMethodTLS = "tls"

	// AuthenticationMethodCandid is a macaroon based authentication method.
	AuthenticationMethodCandid = "candid"

	// AuthenticationMethodOIDC is a token based authentication method.
	AuthenticationMethodOIDC = "oidc"
)

const (
	// IdentityTypeCertificateClientRestricted represents identities that authenticate using TLS and are not privileged.
	IdentityTypeCertificateClientRestricted = "Client certificate (restricted)"

	// IdentityTypeCertificateClientUnrestricted represents identities that authenticate using TLS and are privileged.
	IdentityTypeCertificateClientUnrestricted = "Client certificate (unrestricted)"

	// IdentityTypeCertificateServer represents cluster member authentication.
	IdentityTypeCertificateServer = "Server certificate"

	// IdentityTypeCertificateMetrics represents identities that may only view metrics.
	IdentityTypeCertificateMetrics = "Metrics certificate"
)
