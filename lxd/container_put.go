package main

import (
	"database/sql"
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
	// Get the container
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d.State(), name)
	if err != nil {
		return NotFound
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
	if configRaw.Restore == "" {
		// Update container configuration
		do = func(op *operation) error {
			args := db.ContainerArgs{
				Architecture: architecture,
				Description:  configRaw.Description,
				Config:       configRaw.Config,
				Devices:      configRaw.Devices,
				Ephemeral:    configRaw.Ephemeral,
				Profiles:     configRaw.Profiles}

			// FIXME: should set to true when not migrating
			err = c.Update(args, false)
			if err != nil {
				return err
			}

			return nil
		}
	} else {
		// Snapshot Restore
		do = func(op *operation) error {
			return containerSnapRestore(d.State(), name, configRaw.Restore, configRaw.Stateful)
		}
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerSnapRestore(s *state.State, name string, snap string, stateful bool) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = name + shared.SnapshotDelimiter + snap
	}

	c, err := containerLoadByName(s, name)
	if err != nil {
		return err
	}

	source, err := containerLoadByName(s, snap)
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			return fmt.Errorf("snapshot %s does not exist", snap)
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
