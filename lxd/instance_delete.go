package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
)

func instanceDelete(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Instance is running"))
	}

	rmct := func(op *operations.Operation) error {
		return inst.Delete(false)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationInstanceDelete, resources, nil, rmct, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
