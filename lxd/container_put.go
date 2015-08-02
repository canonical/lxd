package main

import (
	"encoding/json"
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

	var do = func() error { return nil }

	if configRaw.Restore == "" {
		// Update container configuration
		do = func() error {
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
		do = func() error {
			return containerSnapRestore(d, name, configRaw.Restore)
		}
	}

	return AsyncResponse(shared.OperationWrap(do), nil)
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
		shared.Debugf("RESTORE => Error: newLxdContainer() failed for snapshot", err)
		return err
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
