package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
)

func containerDelete(d *Daemon, r *http.Request) response.Response {
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

	if c.IsRunning() {
		return response.BadRequest(fmt.Errorf("container is running"))
	}

	rmct := func(op *operations.Operation) error {
		return c.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operations.OperationCreate(d.cluster, project, operations.OperationClassTask, db.OperationContainerDelete, resources, nil, rmct, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
