package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d.State(), d.Storage, name)
	if err != nil {
		return SmartError(err)
	}

	state, err := c.Render()
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, state)
}
