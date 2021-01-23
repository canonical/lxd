package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func coalesceErrors(errors map[string]error) error {
	if len(errors) == 0 {
		return nil
	}

	errorMsg := "The following instances failed to update state:\n"
	for instName, err := range errors {
		errorMsg += fmt.Sprintf(" - Instance: %s: %v\n", instName, err)
	}

	return fmt.Errorf("%s", errorMsg)
}

// Update all instance states
func instancesPut(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)

	c, err := instance.LoadNodeAll(d.State(), instancetype.Any)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.InstancesPut{}
	req.State = &api.InstanceStatePut{}
	req.State.Timeout = -1
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	action := shared.InstanceAction(req.State.Action)

	var names []string
	var instances []instance.Instance
	for _, inst := range c {
		if inst.Project() != project {
			continue
		}

		switch action {
		case shared.Freeze:
			if !inst.IsRunning() {
				continue
			}
		case shared.Restart:
			if !inst.IsRunning() {
				continue
			}
		case shared.Start:
			if inst.IsRunning() {
				continue
			}
		case shared.Stop:
			if !inst.IsRunning() {
				continue
			}
		case shared.Unfreeze:
			if inst.IsRunning() {
				continue
			}
		}

		instances = append(instances, inst)
		names = append(names, inst.Name())
	}

	// Don't mess with containers while in setup mode
	<-d.readyChan

	// Determine operation type.
	opType, err := instanceActionToOptype(req.State.Action)
	if err != nil {
		return response.BadRequest(err)
	}

	// Batch the changes.
	do := func(op *operations.Operation) error {
		failures := map[string]error{}
		failuresLock := sync.Mutex{}
		wgAction := sync.WaitGroup{}

		for _, inst := range instances {
			wgAction.Add(1)
			go func(inst instance.Instance) {
				defer wgAction.Done()

				err := doInstanceStatePut(inst, *req.State)
				if err != nil {
					failuresLock.Lock()
					failures[inst.Name()] = err
					failuresLock.Unlock()
				}
			}(inst)
		}

		wgAction.Wait()
		return coalesceErrors(failures)
	}

	resources := map[string][]string{}
	resources["instances"] = names
	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
