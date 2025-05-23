package request

import (
	"context"
	"net"

	"github.com/canonical/lxd/shared/api"
)

// CreateRequestor extracts the lifecycle event requestor data from the provided context.
func CreateRequestor(ctx context.Context) *api.EventLifecycleRequestor {
	requestor := &api.EventLifecycleRequestor{}

	reqInfo := GetContextInfo(ctx)
	if reqInfo != nil {
		// Normal requestor.
		requestor.Address = reqInfo.SourceAddress
		requestor.Username = reqInfo.Username
		requestor.Protocol = reqInfo.Protocol

		// Forwarded requestor override.
		if reqInfo.ForwardedAddress != "" {
			requestor.Address = reqInfo.ForwardedAddress
		}

		if reqInfo.ForwardedUsername != "" {
			requestor.Username = reqInfo.ForwardedUsername
		}

		if reqInfo.ForwardedProtocol != "" {
			requestor.Protocol = reqInfo.ForwardedProtocol
		}
	}

	return requestor
}

// SaveConnectionInContext can be set as the ConnContext field of a http.Server to set the connection
// in the request context for later use.
func SaveConnectionInContext(ctx context.Context, connection net.Conn) context.Context {
	return context.WithValue(ctx, CtxConn, connection)
}
