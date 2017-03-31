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

	err = containerValidDevices(d, req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	containers := getContainersWithProfile(d, name)

	// Check if the root device is supposed to be changed or removed.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := containerGetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := containerGetRootDiskDevice(req.Devices)
	if len(containers) > 0 &&
		oldProfileRootDiskDevice["pool"] != "" &&
		newProfileRootDiskDevice["pool"] == "" ||
		(oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {

		// Check for containers using the device
		for _, container := range containers {
			// Check if the device is locally overridden
			localDevices := container.LocalDevices()
			k, v, _ := containerGetRootDiskDevice(localDevices)
			if k != "" && v["pool"] != "" {
				continue
			}

			// Check what profile the device comes from
			profiles := container.Profiles()
			for i := len(profiles) - 1; i >= 0; i-- {
				_, profile, err := dbProfileGet(d.db, profiles[i])
				if err != nil {
					return InternalError(err)
				}

				// Check if we find a match for the device
				_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
				if ok {
					// Found the profile
					if profiles[i] == name {
						// If it's the current profile, then we can't modify that root device
						return BadRequest(fmt.Errorf("At least one container relies on this profile's root disk device."))
					} else {
						// If it's not, then move on to the next container
						break
					}
				}
			}
		}
	}

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
