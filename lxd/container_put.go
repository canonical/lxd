package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) Response {
	project := projectParam(r)

	// Get the container
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
		return NotFound(err)
	}

	// Validate the ETag
	etag := []interface{}{c.Architecture(), c.LocalConfig(), c.LocalDevices(), c.IsEphemeral(), c.Profiles()}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	configRaw := api.ContainerPut{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return BadRequest(err)
	}

	architecture, err := osarch.ArchitectureId(configRaw.Architecture)
	if err != nil {
		architecture = 0
	}

	var do func(*operation) error
	var opType db.OperationType
	if configRaw.Restore == "" {
		// Update container configuration
		do = func(op *operation) error {
			args := db.ContainerArgs{
				Architecture: architecture,
				Config:       configRaw.Config,
				Description:  configRaw.Description,
				Devices:      configRaw.Devices,
				Ephemeral:    configRaw.Ephemeral,
				Profiles:     configRaw.Profiles,
				Project:      project,
			}

			// FIXME: should set to true when not migrating
			err = c.Update(args, false)
			if err != nil {
				return err
			}

			return nil
		}

		opType = db.OperationSnapshotUpdate
	} else {
		// Snapshot Restore
		do = func(op *operation) error {
			return containerSnapRestore(d.State(), project, name, configRaw.Restore, configRaw.Stateful)
		}

		opType = db.OperationSnapshotRestore
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, project, operationClassTask, opType, resources, nil, do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerSnapRestore(s *state.State, project, name, snap string, stateful bool) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = name + shared.SnapshotDelimiter + snap
	}

	c, err := containerLoadByProjectAndName(s, project, name)
	if err != nil {
		return err
	}

	source, err := containerLoadByProjectAndName(s, project, snap)
	if err != nil {
		switch err {
		case db.ErrNoSuchObject:
			return fmt.Errorf("Snapshot %s does not exist", snap)
		default:
			return err
		}
	}

	err = c.Restore(source, stateful)
	if err != nil {
		return err
	}

	return nil
}
