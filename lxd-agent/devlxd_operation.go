package main

import (
	"net/http"
	"net/url"
	"strconv"
)

var devLXDOperationWaitEndpoint = devLXDAPIEndpoint{
	Path: "operations/{id}/wait",
	Get:  devLXDAPIEndpointAction{Handler: devLXDOperationWaitHandler},
}

func devLXDOperationWaitHandler(d *Daemon, r *http.Request) *devLXDResponse {
	opID, err := url.PathUnescape(r.PathValue("id"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	defer client.Disconnect()

	// Determine the timeout based on the timeout query parameter and the request context's deadline.
	timeout := -1
	queryTimeout := r.FormValue("timeout")
	if queryTimeout != "" {
		timeout, err = strconv.Atoi(queryTimeout)
		if err != nil {
			return errorResponse(http.StatusBadRequest, "Invalid timeout value")
		}
	}

	op, etag, err := client.GetOperationWait(opID, timeout)
	if err != nil {
		return smartResponse(err)
	}

	return okResponseETag(op, "json", etag)
}
