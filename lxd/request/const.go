package request

// CtxKey is the type used for all fields stored in the request context by LXD.
type CtxKey string

// Context keys.
const (
	// ctxRequestor is used to access the Requestor.
	ctxRequestor CtxKey = "requestor"

	// CtxDevLXDInstance is the instance that made a request over the devLXD API.
	CtxDevLXDInstance CtxKey = "devlxd_instance"

	// CtxDevLXDOverVsock indicates whether the devLXD is being interacted with over vsock.
	CtxDevLXDOverVsock CtxKey = "devlxd_over_vsock"

	// CtxConn is the connection field in the request context.
	CtxConn CtxKey = "conn"

	// CtxEffectiveProjectName is used to indicate that the effective project of a resource is different from the project
	// specified in the URL. (For example, if a project has `features.networks=false`, any networks in this project actually
	// belong to the default project).
	CtxEffectiveProjectName CtxKey = "effective_project_name"

	// CtxMetricsCallbackFunc is a callback function that can be called to mark the request as completed for the API metrics.
	CtxMetricsCallbackFunc CtxKey = "metrics_callback_function"

	// CtxOpenFGARequestCache is used to set a cache for the OpenFGA datastore to improve driver performance on a per request basis.
	CtxOpenFGARequestCache CtxKey = "openfga_request_cache"
)

// Headers.
const (
	// headerForwardedAddress is the forwarded address field in request header.
	headerForwardedAddress = "X-LXD-forwarded-address"

	// headerForwardedUsername is the forwarded username field in request header.
	headerForwardedUsername = "X-LXD-forwarded-username"

	// headerForwardedProtocol is the forwarded protocol field in request header.
	headerForwardedProtocol = "X-LXD-forwarded-protocol"

	// headerForwardedIdentityProviderGroups is the forwarded identity provider groups field in request header.
	// This will be a JSON marshalled []string.
	headerForwardedIdentityProviderGroups = "X-LXD-forwarded-identity-provider-groups"
)

const (
	// ProtocolCluster is set as the RequestorArgs.Protocol when the request is authenticated via mTLS and the peer
	// certificate is present in the trust store as type [certificate.TypeServer].
	ProtocolCluster string = "cluster"

	// ProtocolUnix is set as the RequestorArgs.Protocol when the request is made over the unix socket.
	ProtocolUnix string = "unix"

	// ProtocolPKI is set as the RequestorArgs.Protocol when a `server.ca` file exists in LXD_DIR, the peer
	// certificate of the request was signed by the CA file, and core.trust_ca_certificates is true.
	//
	// Note: If core.trust_ca_certificates is false, the peer certificate is additionally verified via mTLS and
	// RequestorArgs.Protocol is set to [api.AuthenticationMethodTLS].
	//
	// Note: Regardless of whether `core.trust_ca_certificates` is enabled, if an identity corresponding to the clients
	// peer certificate exists in the [identity.Cache], then protocol should be set to [api.AuthenticationMethodTLS] and
	// the identity should be set as the RequestorArgs.Identity.
	ProtocolPKI string = "pki"

	// ProtocolDevLXD is the authentication method for interacting with the devlxd API.
	ProtocolDevLXD = "devlxd"
)
