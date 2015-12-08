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

type containerPutReq struct {
	Architecture int               `json:"architecture"`
	Config       map[string]string `json:"config"`
	Devices      shared.Devices    `json:"devices"`
	Ephemeral    bool              `json:"ephemeral"`
	Profiles     []string          `json:"profiles"`
	Restore      string            `json:"restore"`
}

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return NotFound
	}

	configRaw := containerPutReq{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return BadRequest(err)
	}

	var do = func(*operation) error { return nil }

	if configRaw.Restore == "" {
		// Update container configuration
		do = func(op *operation) error {
			args := containerArgs{
				Architecture: configRaw.Architecture,
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

	c, err := containerLoadByName(d, name)
	if err != nil {
		shared.Log.Error(
			"RESTORE => loadcontainerLXD() failed",
			log.Ctx{
				"container": name,
				"err":       err})
		return err
	}

	source, err := containerLoadByName(d, snap)
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
