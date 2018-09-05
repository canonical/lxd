package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

func containerGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	state, etag, err := c.Render()
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, state, etag)
}
