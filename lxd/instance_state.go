package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func instanceState(d *Daemon, r *http.Request) response.Response {
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

	c, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}
	state, err := c.RenderState()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, state)
}

func instanceStatePut(d *Daemon, r *http.Request) response.Response {
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

	req := api.InstanceStatePut{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant timeout).
	req.Timeout = -1
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Don't mess with containers while in setup mode.
	<-d.readyChan

	inst, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Actually perform the change.
	opType, err := instanceActionToOptype(req.Action)
	if err != nil {
		return response.BadRequest(err)
	}

	do := func(op *operations.Operation) error {
		inst.SetOperation(op)

		return doInstanceStatePut(inst, req)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}
	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceActionToOptype(action string) (db.OperationType, error) {
	switch shared.InstanceAction(action) {
	case shared.Start:
		return db.OperationInstanceStart, nil
	case shared.Stop:
		return db.OperationInstanceStop, nil
	case shared.Restart:
		return db.OperationInstanceRestart, nil
	case shared.Freeze:
		return db.OperationInstanceFreeze, nil
	case shared.Unfreeze:
		return db.OperationInstanceUnfreeze, nil
	}

	return db.OperationUnknown, fmt.Errorf("Unknown action: '%s'", action)
}

func doInstanceStatePut(inst instance.Instance, req api.InstanceStatePut) error {
	switch shared.InstanceAction(req.Action) {
	case shared.Start:
		return inst.Start(req.Stateful)
	case shared.Stop:
		if req.Stateful {
			return inst.Stop(req.Stateful)
		} else if req.Timeout == 0 || req.Force {
			return inst.Stop(false)
		} else {
			return inst.Shutdown(time.Duration(req.Timeout) * time.Second)
		}
	case shared.Restart:
		timeout := req.Timeout
		if req.Force {
			timeout = 0
		}

		return inst.Restart(time.Duration(timeout))
	case shared.Freeze:
		return inst.Freeze()
	case shared.Unfreeze:
		return inst.Unfreeze()
	}

	return fmt.Errorf("Unknown action: '%s'", req.Action)
}
