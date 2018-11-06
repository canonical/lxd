package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func containerState(d *Daemon, r *http.Request) Response {
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
	state, err := c.RenderState()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, state)
}

func containerStatePut(d *Daemon, r *http.Request) Response {
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

	raw := api.ContainerStatePut{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant
	// timeout).
	raw.Timeout = -1

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	// Don't mess with containers while in setup mode
	<-d.readyChan

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	var opType db.OperationType
	var do func(*operation) error
	switch shared.ContainerAction(raw.Action) {
	case shared.Start:
		opType = db.OperationContainerStart
		do = func(op *operation) error {
			c.SetOperation(op)
			if err = c.Start(raw.Stateful); err != nil {
				return err
			}
			return nil
		}
	case shared.Stop:
		opType = db.OperationContainerStop
		if raw.Stateful {
			do = func(op *operation) error {
				c.SetOperation(op)
				err := c.Stop(raw.Stateful)
				if err != nil {
					return err
				}

				return nil
			}
		} else if raw.Timeout == 0 || raw.Force {
			do = func(op *operation) error {
				c.SetOperation(op)
				err = c.Stop(false)
				if err != nil {
					return err
				}

				return nil
			}
		} else {
			do = func(op *operation) error {
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
		opType = db.OperationContainerRestart
		do = func(op *operation) error {
			c.SetOperation(op)
			ephemeral := c.IsEphemeral()

			if ephemeral {
				// Unset ephemeral flag
				args := db.ContainerArgs{
					Architecture: c.Architecture(),
					Config:       c.LocalConfig(),
					Description:  c.Description(),
					Devices:      c.LocalDevices(),
					Ephemeral:    false,
					Profiles:     c.Profiles(),
					Project:      c.Project(),
				}

				err := c.Update(args, false)
				if err != nil {
					return err
				}

				// On function return, set the flag back on
				defer func() {
					args.Ephemeral = ephemeral
					c.Update(args, true)
				}()
			}

			if raw.Timeout == 0 || raw.Force {
				err = c.Stop(false)
				if err != nil {
					return err
				}
			} else {
				if c.IsFrozen() {
					return fmt.Errorf("container is not running")
				}

				err = c.Shutdown(time.Duration(raw.Timeout) * time.Second)
				if err != nil {
					return err
				}
			}

			err = c.Start(false)
			if err != nil {
				return err
			}

			return nil
		}
	case shared.Freeze:
		if !d.os.CGroupFreezerController {
			return BadRequest(fmt.Errorf("This system doesn't support freezing containers"))
		}

		opType = db.OperationContainerFreeze
		do = func(op *operation) error {
			c.SetOperation(op)
			return c.Freeze()
		}
	case shared.Unfreeze:
		if !d.os.CGroupFreezerController {
			return BadRequest(fmt.Errorf("This system doesn't support unfreezing containers"))
		}

		opType = db.OperationContainerUnfreeze
		do = func(op *operation) error {
			c.SetOperation(op)
			return c.Unfreeze()
		}
	default:
		return BadRequest(fmt.Errorf("unknown action %s", raw.Action))
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, project, operationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
