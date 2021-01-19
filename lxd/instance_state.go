package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/cgroup"
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

	raw := api.InstanceStatePut{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant
	// timeout).
	raw.Timeout = -1

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return response.BadRequest(err)
	}

	// Don't mess with containers while in setup mode
	<-d.readyChan

	c, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	var opType db.OperationType
	var do func(*operations.Operation) error
	switch shared.InstanceAction(raw.Action) {
	case shared.Start:
		opType = db.OperationInstanceStart
		do = func(op *operations.Operation) error {
			c.SetOperation(op)
			if err = c.Start(raw.Stateful); err != nil {
				return err
			}
			return nil
		}
	case shared.Stop:
		opType = db.OperationInstanceStop
		if raw.Stateful {
			do = func(op *operations.Operation) error {
				c.SetOperation(op)
				err := c.Stop(raw.Stateful)
				if err != nil {
					return err
				}

				return nil
			}
		} else if raw.Timeout == 0 || raw.Force {
			do = func(op *operations.Operation) error {
				c.SetOperation(op)
				err = c.Stop(false)
				if err != nil {
					return err
				}

				return nil
			}
		} else {
			do = func(op *operations.Operation) error {
				c.SetOperation(op)
				if c.IsFrozen() {
					err := c.Unfreeze()
					if err != nil {
						return err
					}
				}

				err = c.Shutdown(time.Duration(raw.Timeout) * time.Second)
				if err != nil {
					return err
				}

				return nil
			}
		}
	case shared.Restart:
		opType = db.OperationInstanceRestart
		do = func(op *operations.Operation) error {
			c.SetOperation(op)
			ephemeral := c.IsEphemeral()

			if ephemeral {
				// Unset ephemeral flag
				args := db.InstanceArgs{
					Architecture: c.Architecture(),
					Config:       c.LocalConfig(),
					Description:  c.Description(),
					Devices:      c.LocalDevices(),
					Ephemeral:    false,
					Profiles:     c.Profiles(),
					Project:      c.Project(),
					Type:         c.Type(),
					Snapshot:     c.IsSnapshot(),
				}

				err := c.Update(args, false)
				if err != nil {
					return err
				}

				// On function return, set the flag back on
				defer func() {
					args.Ephemeral = ephemeral
					c.Update(args, false)
				}()
			}

			timeout := raw.Timeout
			if raw.Force {
				timeout = 0
			}
			return c.Restart(time.Duration(timeout))
		}
	case shared.Freeze:
		if !d.os.CGInfo.Supports(cgroup.Freezer, nil) {
			return response.BadRequest(fmt.Errorf("This system doesn't support freezing instances"))
		}

		opType = db.OperationInstanceFreeze
		do = func(op *operations.Operation) error {
			c.SetOperation(op)
			return c.Freeze()
		}
	case shared.Unfreeze:
		if !d.os.CGInfo.Supports(cgroup.Freezer, nil) {
			return response.BadRequest(fmt.Errorf("This system doesn't support unfreezing instances"))
		}

		opType = db.OperationInstanceUnfreeze
		do = func(op *operations.Operation) error {
			c.SetOperation(op)
			return c.Unfreeze()
		}
	default:
		return response.BadRequest(fmt.Errorf("Unknown action %s", raw.Action))
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
