package request

type requestCtxKey string

const (
	// CtxAccess is the access field in request context.
	CtxAccess = "access"

	// CtxConn is the access field in the request context
	CtxConn requestCtxKey = "conn"

	// CtxAddress is the address field in request context.
	CtxAddress = "address"

	// CtxUsername is the username field in request context.
	CtxUsername = "username"

	// CtxProtocol is the protocol field in request context.
	CtxProtocol = "protocol"

	// CtxForwardedAddress is the forwarded address field in request context.
	CtxForwardedAddress = "forwarded_address"

	// CtxForwardedUsername is the forwarded username field in request context.
	CtxForwardedUsername = "forwarded_username"

	// CtxForwardedProtocol is the forwarded protocol field in request context.
	CtxForwardedProtocol = "forwarded_protocol"

	// HeaderForwardedAddress is the forwarded address field in request header.
	HeaderForwardedAddress = "X-LXD-forwarded-address"

	// HeaderForwardedUsername is the forwarded username field in request header.
	HeaderForwardedUsername = "X-LXD-forwarded-username"

	// HeaderForwardedProtocol is the forwarded protocol field in request header.
	HeaderForwardedProtocol = "X-LXD-forwarded-protocol"
)
