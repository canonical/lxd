package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func coalesceErrors(local bool, errors map[string]error) error {
	if len(errors) == 0 {
		return nil
	}

	var errorMsg string
	if local {
		errorMsg += "The following instances failed to update state:\n"
	}

	for instName, err := range errors {
		if local {
			errorMsg += fmt.Sprintf(" - Instance: %s: %v\n", instName, err)
		} else {
			errorMsg += strings.TrimSpace(fmt.Sprintf("%v\n", err))
		}
	}

	return fmt.Errorf("%s", errorMsg)
}

// Update all instance states
func instancesPut(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)

	// Don't mess with containers while in setup mode
	<-d.readyChan

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

	// Determine operation type.
	opType, err := instanceActionToOptype(req.State.Action)
	if err != nil {
		return response.BadRequest(err)
	}

	// Batch the changes.
	do := func(op *operations.Operation) error {
		localAction := func(local bool) error {
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
			return coalesceErrors(local, failures)
		}

		// Only return the local data if asked by cluster member.
		if isClusterNotification(r) {
			return localAction(false)
		}

		// Check if clustered.
		clustered, err := cluster.Enabled(d.db)
		if err != nil {
			return err
		}

		// If not clustered, return the local data.
		if !clustered {
			return localAction(true)
		}

		// Get all online nodes.
		var nodes []db.NodeInfo
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error

			nodes, err = tx.GetNodes()
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Get local address.
		localAddress, err := node.HTTPSAddress(d.db)
		if err != nil {
			return err
		}

		// Record the results.
		failures := map[string]error{}
		failuresLock := sync.Mutex{}
		wgAction := sync.WaitGroup{}

		cert := d.endpoints.NetworkCert()
		for _, node := range nodes {
			wgAction.Add(1)
			go func(node db.NodeInfo) {
				defer wgAction.Done()

				// Special handling for the local node.
				if node.Address == localAddress {
					err := localAction(false)
					if err != nil {
						failuresLock.Lock()
						failures[node.Name] = err
						failuresLock.Unlock()
					}
					return
				}

				// Connect to the remote server.
				client, err := cluster.Connect(node.Address, cert, true)
				if err != nil {
					failuresLock.Lock()
					failures[node.Name] = err
					failuresLock.Unlock()
					return
				}
				client = client.UseProject(project)

				// Perform the action.
				op, err := client.UpdateInstances(req, "")
				if err != nil {
					failuresLock.Lock()
					failures[node.Name] = err
					failuresLock.Unlock()
					return
				}

				err = op.Wait()
				if err != nil {
					failuresLock.Lock()
					failures[node.Name] = err
					failuresLock.Unlock()
					return
				}
			}(node)
		}

		wgAction.Wait()
		return coalesceErrors(true, failures)
	}

	resources := map[string][]string{}
	resources["instances"] = names
	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
