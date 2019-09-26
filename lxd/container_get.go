package main

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/response"
)

func containerGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	state, etag, err := c.Render()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, state, etag)
}
