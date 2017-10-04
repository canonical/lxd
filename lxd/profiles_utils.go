package main

import (
	"fmt"
	"reflect"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared/api"
)

func doProfileUpdate(d *Daemon, name string, id int64, profile *api.Profile, req api.ProfilePut) Response {
	// Sanity checks
	err := containerValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	containers := getContainersWithProfile(d.State(), d.Storage, name)

	// Update the database
	tx, err := db.Begin(d.db)
	if err != nil {
		return SmartError(err)
	}

	if profile.Description != req.Description {
		err = db.ProfileDescriptionUpdate(tx, id, req.Description)
		if err != nil {
			tx.Rollback()
			return SmartError(err)
		}
	}

	// Optimize for description-only changes
	if reflect.DeepEqual(profile.Config, req.Config) && reflect.DeepEqual(profile.Devices, req.Devices) {
		err = db.TxCommit(tx)
		if err != nil {
			return SmartError(err)
		}

		return EmptySyncResponse
	}

	err = db.ProfileConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = db.ProfileConfigAdd(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = db.DevicesAdd(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = db.TxCommit(tx)
	if err != nil {
		return SmartError(err)
	}

	// Update all the containers using the profile. Must be done after db.TxCommit due to DB lock.
	failures := map[string]error{}
	for _, c := range containers {
		err = c.Update(db.ContainerArgs{
			Architecture: c.Architecture(),
			Ephemeral:    c.IsEphemeral(),
			Config:       c.LocalConfig(),
			Devices:      c.LocalDevices(),
			Profiles:     c.Profiles()}, true)

		if err != nil {
			failures[c.Name()] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following containers failed to update (profile change still saved):\n"
		for cname, err := range failures {
			msg += fmt.Sprintf(" - %s: %s\n", cname, err)
		}
		return SmartError(fmt.Errorf("%s", msg))
	}

	return EmptySyncResponse
}
