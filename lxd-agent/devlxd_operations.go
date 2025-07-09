package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
)

var devLXDOperationsWaitEndpoint = devLXDAPIEndpoint{
	Path: "operations/{id}/wait",
	Get:  devLXDAPIEndpointAction{Handler: devLXDOperationsWaitGetHandler},
}

func devLXDOperationsWaitGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	opID, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	defer client.Disconnect()

	// Get timeout from the query parameter.
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
