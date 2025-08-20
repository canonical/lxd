package request

import (
	"context"
	"net"

	"github.com/canonical/lxd/shared/api"
)

// CreateRequestor extracts the lifecycle event requestor data from the provided context.
func CreateRequestor(ctx context.Context) *api.EventLifecycleRequestor {
	requestor, err := GetRequestor(ctx)
	if err != nil {
		return &api.EventLifecycleRequestor{}
	}

	return requestor.EventLifecycleRequestor()
}

// SaveConnectionInContext can be set as the ConnContext field of a http.Server to set the connection
// in the request context for later use.
func SaveConnectionInContext(ctx context.Context, connection net.Conn) context.Context {
	return context.WithValue(ctx, CtxConn, connection)
}
