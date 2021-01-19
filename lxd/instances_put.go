package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
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

	c, err := instance.LoadByProject(d.State(), project)
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

	// Batch the actions.
	failures := map[string]error{}
	failuresLock := sync.Mutex{}
	wgAction := sync.WaitGroup{}

	var opType db.OperationType
	var do func(*operations.Operation) error
	switch action {
	case shared.Start:
		opType = db.OperationInstanceStart
		do = func(op *operations.Operation) error {
			for _, inst := range instances {
				wgAction.Add(1)
				go func(inst instance.Instance) {
					defer wgAction.Done()

					err = inst.Start(req.State.Stateful)
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
	case shared.Stop:
		opType = db.OperationInstanceStop
		if req.State.Stateful {
			do = func(op *operations.Operation) error {
				for _, inst := range instances {
					wgAction.Add(1)
					go func(inst instance.Instance) {
						defer wgAction.Done()

						err = inst.Stop(req.State.Stateful)
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
		} else if req.State.Timeout == 0 || req.State.Force {
			do = func(op *operations.Operation) error {
				for _, inst := range instances {
					wgAction.Add(1)
					go func(inst instance.Instance) {
						defer wgAction.Done()

						err = inst.Stop(false)
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
		} else {
			do = func(op *operations.Operation) error {
				for _, inst := range instances {
					wgAction.Add(1)
					go func(inst instance.Instance) {
						defer wgAction.Done()

						if inst.IsFrozen() {
							err := inst.Unfreeze()
							if err != nil {
								failuresLock.Lock()
								failures[inst.Name()] = err
								failuresLock.Unlock()
								return
							}
						}

						err = inst.Shutdown(time.Duration(req.State.Timeout) * time.Second)
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
		}
	case shared.Restart:
		opType = db.OperationInstanceRestart
		do = func(op *operations.Operation) error {
			for _, inst := range instances {
				wgAction.Add(1)
				go func(inst instance.Instance) {
					defer wgAction.Done()

					ephemeral := inst.IsEphemeral()
					if ephemeral {
						// Unset ephemeral flag
						args := db.InstanceArgs{
							Architecture: inst.Architecture(),
							Config:       inst.LocalConfig(),
							Description:  inst.Description(),
							Devices:      inst.LocalDevices(),
							Ephemeral:    false,
							Profiles:     inst.Profiles(),
							Project:      inst.Project(),
							Type:         inst.Type(),
							Snapshot:     inst.IsSnapshot(),
						}

						err := inst.Update(args, false)
						if err != nil {
							failuresLock.Lock()
							failures[inst.Name()] = err
							failuresLock.Unlock()
						}

						// On function return, set the flag back on
						defer func() {
							args.Ephemeral = ephemeral
							inst.Update(args, false)
						}()
					}

					timeout := req.State.Timeout
					if req.State.Force {
						timeout = 0
					}

					res := inst.Restart(time.Duration(timeout))
					if res != nil {
						failuresLock.Lock()
						failures[inst.Name()] = err
						failuresLock.Unlock()
					}
				}(inst)
			}

			wgAction.Wait()
			return coalesceErrors(failures)
		}
	case shared.Freeze:
		if !d.os.CGInfo.Supports(cgroup.Freezer, nil) {
			return response.BadRequest(fmt.Errorf("This system doesn't support freezing instances"))
		}

		opType = db.OperationInstanceFreeze
		do = func(op *operations.Operation) error {
			for _, inst := range instances {
				wgAction.Add(1)
				go func(inst instance.Instance) {
					defer wgAction.Done()

					err = inst.Freeze()
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
	case shared.Unfreeze:
		if !d.os.CGInfo.Supports(cgroup.Freezer, nil) {
			return response.BadRequest(fmt.Errorf("This system doesn't support unfreezing instances"))
		}

		opType = db.OperationInstanceUnfreeze
		do = func(op *operations.Operation) error {
			for _, inst := range instances {
				wgAction.Add(1)
				go func(inst instance.Instance) {
					defer wgAction.Done()

					err = inst.Unfreeze()
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
	default:
		return response.BadRequest(fmt.Errorf("Unknown action %s", req.State.Action))
	}

	resources := map[string][]string{}
	resources["instances"] = names

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
