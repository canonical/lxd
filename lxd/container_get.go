package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/response"
)

func containerGet(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return response.SmartError(err)
	}

	state, etag, err := c.Render()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponseETag(true, state, etag)
}
