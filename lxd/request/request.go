package request

import (
	"net/http"

	"github.com/lxc/lxd/shared/api"
)

// CreateRequestor extracts the lifecycle event requestor data from an http.Request context
func CreateRequestor(r *http.Request) *api.EventLifecycleRequestor {
	ctx := r.Context()
	requestor := &api.EventLifecycleRequestor{}

	// Normal requestor.
	val, ok := ctx.Value(CtxUsername).(string)
	if ok {
		requestor.Username = val
	}

	val, ok = ctx.Value(CtxProtocol).(string)
	if ok {
		requestor.Protocol = val
	}

	// Forwarded requestor override.
	val, ok = ctx.Value(CtxForwardedUsername).(string)
	if ok {
		requestor.Username = val
	}

	val, ok = ctx.Value(CtxForwardedProtocol).(string)
	if ok {
		requestor.Protocol = val
	}
	return requestor
}
