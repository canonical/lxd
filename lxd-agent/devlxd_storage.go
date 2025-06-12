package main

import (
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
)

var devLXDStoragePoolEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{pool}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolGetHandler},
}

func devLXDStoragePoolGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	defer client.Disconnect()

	pool, etag, err := client.GetStoragePool(poolName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponseETag(pool, "json", etag)
}
