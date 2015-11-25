package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLXDLoad(d, name)
	if err != nil {
		return NotFound
	}

	configRaw := containerConfigReq{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return BadRequest(err)
	}

	if configRaw.Devices == nil {
		configRaw.Devices = shared.Devices{}
	}

	if configRaw.Config == nil {
		configRaw.Config = map[string]string{}
	}

	var do = func(*operation) error { return nil }

	if configRaw.Restore == "" {
		// Update container configuration
		do = func(op *operation) error {
			args := containerLXDArgs{
				Config:   configRaw.Config,
				Devices:  configRaw.Devices,
				Profiles: configRaw.Profiles}
			err = c.ConfigReplace(args)
			if err != nil {
				return err
			}

			return nil
		}
	} else {
		// Snapshot Restore
		do = func(op *operation) error {
			return containerSnapRestore(d, name, configRaw.Restore)
		}
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(operationClassTask, resources, nil, do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerSnapRestore(d *Daemon, name string, snap string) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = name + shared.SnapshotDelimiter + snap
	}

	shared.Log.Info(
		"RESTORE => Restoring snapshot",
		log.Ctx{
			"snapshot":  snap,
			"container": name})

	c, err := containerLXDLoad(d, name)
	if err != nil {
		shared.Log.Error(
			"RESTORE => loadcontainerLXD() failed",
			log.Ctx{
				"container": name,
				"err":       err})
		return err
	}

	source, err := containerLXDLoad(d, snap)
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			return fmt.Errorf("snapshot %s does not exist", snap)
		default:
			return err
		}
	}

	if err := c.Restore(source); err != nil {
		return err
	}

	return nil
}

func emptyProfile(l []string) bool {
	if len(l) == 0 {
		return true
	}
	if len(l) == 1 && l[0] == "" {
		return true
	}
	return false
}
