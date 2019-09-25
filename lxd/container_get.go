package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/instance"
)

func containerGet(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
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

	c, err := instance.InstanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	state, etag, err := c.Render()
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, state, etag)
}
