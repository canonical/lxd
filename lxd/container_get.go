package main

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/instance"
)

func containerGet(d *Daemon, r *http.Request) Response {
	// Instance type.
	instanceType := instance.TypeAny
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		instanceType = instance.TypeContainer
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
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
