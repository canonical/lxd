package main

import (
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
)

var devLXDInstanceEndpoint = devLXDAPIEndpoint{
	Path: "instances/{name}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDInstanceGetHandler},
}

func devLXDInstanceGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	instName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	defer client.Disconnect()

	inst, etag, err := client.GetInstance(instName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponseETag(inst, "json", etag)
}
