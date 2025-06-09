package request

// CtxKey is the type used for all fields stored in the request context by LXD.
type CtxKey string

// Context keys.
const (
	// CtxRequestInfo is the request information that are stored in the request context.
	CtxRequestInfo CtxKey = "request_info"

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
	// HeaderForwardedAddress is the forwarded address field in request header.
	HeaderForwardedAddress = "X-LXD-forwarded-address"

	// HeaderForwardedUsername is the forwarded username field in request header.
	HeaderForwardedUsername = "X-LXD-forwarded-username"

	// HeaderForwardedProtocol is the forwarded protocol field in request header.
	HeaderForwardedProtocol = "X-LXD-forwarded-protocol"

	// HeaderForwardedIdentityProviderGroups is the forwarded identity provider groups field in request header.
	// This will be a JSON marshalled []string.
	HeaderForwardedIdentityProviderGroups = "X-LXD-forwarded-identity-provider-groups"
)
