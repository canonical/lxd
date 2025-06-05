package request

// CtxKey is the type used for all fields stored in the request context by LXD.
type CtxKey string

// Context keys.
const (
	// CtxDevLXDInstance is the instance that made a request over the devLXD API.
	CtxDevLXDInstance CtxKey = "devlxd_instance"

	// CtxDevLXDOverVsock indicates whether the devLXD is being interacted with over vsock.
	CtxDevLXDOverVsock CtxKey = "devlxd_over_vsock"

	// CtxAccess is the access field in request context.
	CtxAccess CtxKey = "access"

	// CtxConn is the connection field in the request context.
	CtxConn CtxKey = "conn"

	// CtxAddress is the address field in request context.
	CtxAddress CtxKey = "address"

	// CtxUsername is the username field in request context.
	CtxUsername CtxKey = "username"

	// CtxProtocol is the protocol field in request context.
	CtxProtocol CtxKey = "protocol"

	// CtxIdentityProviderGroups is the identity provider groups field in the request context.
	// This contains groups defined by the identity provider if the identity authenticated with OIDC.
	CtxIdentityProviderGroups CtxKey = "identity_provider_groups"

	// CtxForwardedAddress is the forwarded address field in request context.
	CtxForwardedAddress CtxKey = "forwarded_address"

	// CtxForwardedUsername is the forwarded username field in request context.
	CtxForwardedUsername CtxKey = "forwarded_username"

	// CtxForwardedProtocol is the forwarded protocol field in request context.
	CtxForwardedProtocol CtxKey = "forwarded_protocol"

	// CtxForwardedIdentityProviderGroups is the identity provider groups field in the request context.
	// This contains groups defined by the identity provider if the identity authenticated with OIDC on another cluster
	// member.
	CtxForwardedIdentityProviderGroups CtxKey = "identity_provider_groups"

	// CtxEffectiveProjectName is used to indicate that the effective project of a resource is different from the project
	// specified in the URL. (For example, if a project has `features.networks=false`, any networks in this project actually
	// belong to the default project).
	CtxEffectiveProjectName CtxKey = "effective_project_name"

	// CtxTrusted is a boolean value that indicates whether the request was authenticated or not.
	CtxTrusted CtxKey = "trusted"

	// CtxMetricsCallbackFunc is a callback function that can be called to mark the request as completed for the API metrics.
	CtxMetricsCallbackFunc CtxKey = "metrics_callback_function"

	// CtxOpenFGARequestCache is used to set a cache for the OpenFGA datastore to improve driver performance on a per request basis.
	CtxOpenFGARequestCache CtxKey = "openfga_request_cache"

	// CtxRequestSourceAddress is the request's source address.
	CtxRequestSourceAddress CtxKey = "request_source_address"
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
