package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLXDLoad(d, name)
	if err != nil {
		return SmartError(err)
	}

	state, err := c.RenderState()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, state)
}
