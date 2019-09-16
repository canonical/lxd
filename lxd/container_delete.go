package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db"
)

func containerDelete(d *Daemon, r *http.Request) Response {
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

	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	if c.IsRunning() {
		return BadRequest(fmt.Errorf("container is running"))
	}

	rmct := func(op *operation) error {
		return c.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationContainerDelete, resources, nil, rmct, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
