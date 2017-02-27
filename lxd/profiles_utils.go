package main

import (
	"fmt"
	"reflect"

	"github.com/lxc/lxd/shared/api"
)

func doProfileUpdate(d *Daemon, name string, id int64, profile *api.Profile, req api.ProfilePut) Response {
	// Sanity checks
	err := containerValidConfig(d, req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	containers := getContainersWithProfile(d, name)

	// Update the database
	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	if profile.Description != req.Description {
		err = dbProfileDescriptionUpdate(tx, id, req.Description)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	// Optimize for description-only changes
	if reflect.DeepEqual(profile.Config, req.Config) && reflect.DeepEqual(profile.Devices, req.Devices) {
		err = txCommit(tx)
		if err != nil {
			return InternalError(err)
		}

		return EmptySyncResponse
	}

	err = dbProfileConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	err = dbProfileConfigAdd(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = dbDevicesAdd(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = txCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	// Update all the containers using the profile. Must be done after txCommit due to DB lock.
	failures := map[string]error{}
	for _, c := range containers {
		err = c.Update(containerArgs{
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
		return InternalError(fmt.Errorf("%s", msg))
	}

	return EmptySyncResponse
}
