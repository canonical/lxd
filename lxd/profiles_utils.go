package main

import (
	"fmt"
	"reflect"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func doProfileUpdate(d *Daemon, name string, id int64, profile *api.Profile, req api.ProfilePut) error {
	// Sanity checks
	err := containerValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return err
	}

	err = containerValidDevices(d.cluster, req.Devices, true, false)
	if err != nil {
		return err
	}

	containers := getContainersWithProfile(d.State(), name)

	// Check if the root device is supposed to be changed or removed.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := shared.GetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if len(containers) > 0 && oldProfileRootDiskDevice["pool"] != "" && newProfileRootDiskDevice["pool"] == "" || (oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {
		// Check for containers using the device
		for _, container := range containers {
			// Check if the device is locally overridden
			localDevices := container.LocalDevices()
			k, v, _ := shared.GetRootDiskDevice(localDevices)
			if k != "" && v["pool"] != "" {
				continue
			}

			// Check what profile the device comes from
			profiles := container.Profiles()
			for i := len(profiles) - 1; i >= 0; i-- {
				_, profile, err := d.cluster.ProfileGet(profiles[i])
				if err != nil {
					return err
				}

				// Check if we find a match for the device
				_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
				if ok {
					// Found the profile
					if profiles[i] == name {
						// If it's the current profile, then we can't modify that root device
						return fmt.Errorf("At least one container relies on this profile's root disk device.")
					} else {
						// If it's not, then move on to the next container
						break
					}
				}
			}
		}
	}

	// Update the database
	tx, err := d.cluster.Begin()
	if err != nil {
		return err
	}

	if profile.Description != req.Description {
		err = db.ProfileDescriptionUpdate(tx, id, req.Description)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// Optimize for description-only changes
	if reflect.DeepEqual(profile.Config, req.Config) && reflect.DeepEqual(profile.Devices, req.Devices) {
		err = db.TxCommit(tx)
		if err != nil {
			return err
		}

		return nil
	}

	err = db.ProfileConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = db.ProfileConfigAdd(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = db.DevicesAdd(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = db.TxCommit(tx)
	if err != nil {
		return err
	}

	// Update all the containers using the profile. Must be done after db.TxCommit due to DB lock.
	failures := map[string]error{}
	for _, c := range containers {
		err = c.Update(db.ContainerArgs{
			Architecture: c.Architecture(),
			Ephemeral:    c.IsEphemeral(),
			Config:       c.LocalConfig(),
			Devices:      c.LocalDevices(),
			Profiles:     c.Profiles(),
			Description:  c.Description(),
		}, true)

		if err != nil {
			failures[c.Name()] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following containers failed to update (profile change still saved):\n"
		for cname, err := range failures {
			msg += fmt.Sprintf(" - %s: %s\n", cname, err)
		}
		return fmt.Errorf("%s", msg)
	}

	return nil
}
