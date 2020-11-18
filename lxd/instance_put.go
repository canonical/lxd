package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/grant-he/lxd/lxd/db"
	deviceConfig "github.com/grant-he/lxd/lxd/device/config"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/operations"
	projecthelpers "github.com/grant-he/lxd/lxd/project"
	"github.com/grant-he/lxd/lxd/response"
	"github.com/grant-he/lxd/lxd/state"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/osarch"
)

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)

	// Get the container
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
		return response.NotFound(err)
	}

	// Validate the ETag
	etag := []interface{}{c.Architecture(), c.LocalConfig(), c.LocalDevices(), c.IsEphemeral(), c.Profiles()}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	configRaw := api.InstancePut{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return response.BadRequest(err)
	}

	architecture, err := osarch.ArchitectureId(configRaw.Architecture)
	if err != nil {
		architecture = 0
	}

	// Check project limits.
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return projecthelpers.AllowInstanceUpdate(tx, project, name, configRaw, c.LocalConfig())
	})
	if err != nil {
		return response.SmartError(err)
	}

	var do func(*operations.Operation) error
	var opType db.OperationType
	if configRaw.Restore == "" {
		// Update container configuration
		do = func(op *operations.Operation) error {
			args := db.InstanceArgs{
				Architecture: architecture,
				Config:       configRaw.Config,
				Description:  configRaw.Description,
				Devices:      deviceConfig.NewDevices(configRaw.Devices),
				Ephemeral:    configRaw.Ephemeral,
				Profiles:     configRaw.Profiles,
				Project:      project,
			}

			err = c.Update(args, true)
			if err != nil {
				return err
			}

			return nil
		}

		opType = db.OperationContainerUpdate
	} else {
		// Snapshot Restore
		do = func(op *operations.Operation) error {
			return instanceSnapRestore(d.State(), project, name, configRaw.Restore, configRaw.Stateful)
		}

		opType = db.OperationSnapshotRestore
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceSnapRestore(s *state.State, project, name, snap string, stateful bool) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = name + shared.SnapshotDelimiter + snap
	}

	inst, err := instance.LoadByProjectAndName(s, project, name)
	if err != nil {
		return err
	}

	source, err := instance.LoadByProjectAndName(s, project, snap)
	if err != nil {
		switch err {
		case db.ErrNoSuchObject:
			return fmt.Errorf("Snapshot %s does not exist", snap)
		default:
			return err
		}
	}

	err = inst.Restore(source, stateful)
	if err != nil {
		return err
	}

	return nil
}
