package request

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/shared/api"
)

// CreateRequestor extracts the lifecycle event requestor data from an http.Request context.
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

	requestor.Address = r.RemoteAddr

	// Forwarded requestor override.
	val, ok = ctx.Value(CtxForwardedUsername).(string)
	if ok {
		requestor.Username = val
	}

	val, ok = ctx.Value(CtxForwardedProtocol).(string)
	if ok {
		requestor.Protocol = val
	}

	val, ok = ctx.Value(CtxForwardedAddress).(string)
	if ok {
		requestor.Address = val
	}

	return requestor
}

// SaveConnectionInContext can be set as the ConnContext field of a http.Server to set the connection
// in the request context for later use.
func SaveConnectionInContext(ctx context.Context, connection net.Conn) context.Context {
	return context.WithValue(ctx, CtxConn, connection)
}

// CountStartedRequest tracks the request as started for the API metrics and
// injects a callback function to track the request as completed.
func CountStartedRequest(r *http.Request) {
	requestURL := *r.URL

	// Set the callback function to track the request as completed.
	// Use sync.Once to ensure it can be called at most once.
	var once sync.Once
	callbackFunc := func(result metrics.RequestResult) {
		once.Do(func() {
			metrics.TrackCompletedRequest(requestURL, result)
		})
	}

	SetCtxValue(r, MetricsCallbackFunc, callbackFunc)

	metrics.TrackStartedRequest(requestURL)
}
