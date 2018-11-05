package main

import (
	"fmt"
	"reflect"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

func doProfileUpdate(d *Daemon, project, name string, id int64, profile *api.Profile, req api.ProfilePut) error {
	// Sanity checks
	err := containerValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return err
	}

	err = containerValidDevices(d.cluster, req.Devices, true, false)
	if err != nil {
		return err
	}

	containers, err := getProfileContainersInfo(d.cluster, project, name)
	if err != nil {
		return errors.Wrapf(err, "failed to query containers associated with profile '%s'", name)
	}

	// Check if the root device is supposed to be changed or removed.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := shared.GetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if len(containers) > 0 && oldProfileRootDiskDevice["pool"] != "" && newProfileRootDiskDevice["pool"] == "" || (oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {
		// Check for containers using the device
		for _, container := range containers {
			// Check if the device is locally overridden
			k, v, _ := shared.GetRootDiskDevice(container.Devices)
			if k != "" && v["pool"] != "" {
				continue
			}

			// Check what profile the device comes from
			profiles := container.Profiles
			for i := len(profiles) - 1; i >= 0; i-- {
				_, profile, err := d.cluster.ProfileGet("default", profiles[i])
				if err != nil {
					return err
				}

				// Check if we find a match for the device
				_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
				if ok {
					// Found the profile
					if profiles[i] == name {
						// If it's the current profile, then we can't modify that root device
						return fmt.Errorf("At least one container relies on this profile's root disk device")
					} else {
						// If it's not, then move on to the next container
						break
					}
				}
			}
		}
	}

	// Update the database
	err = query.Retry(func() error {
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
		return nil
	})
	if err != nil {
		return err
	}

	// Update all the containers on this node using the profile. Must be
	// done after db.TxCommit due to DB lock.
	nodeName := ""
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		nodeName, err = tx.NodeName()
		return err
	})
	if err != nil {
		return errors.Wrap(err, "failed to query local node name")
	}
	failures := map[string]error{}
	for _, args := range containers {
		err := doProfileUpdateContainer(d, name, profile.ProfilePut, nodeName, args)
		if err != nil {
			failures[args.Name] = err
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

// Like doProfileUpdate but does not update the database, since it was already
// updated by doProfileUpdate itself, called on the notifying node.
func doProfileUpdateCluster(d *Daemon, project, name string, old api.ProfilePut) error {
	nodeName := ""
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		nodeName, err = tx.NodeName()
		return err
	})
	if err != nil {
		return errors.Wrap(err, "failed to query local node name")
	}

	containers, err := getProfileContainersInfo(d.cluster, project, name)
	if err != nil {
		return errors.Wrapf(err, "failed to query containers associated with profile '%s'", name)
	}

	failures := map[string]error{}
	for _, args := range containers {
		err := doProfileUpdateContainer(d, name, old, nodeName, args)
		if err != nil {
			failures[args.Name] = err
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

// Profile update of a single container.
func doProfileUpdateContainer(d *Daemon, name string, old api.ProfilePut, nodeName string, args db.ContainerArgs) error {
	if args.Node != "" && args.Node != nodeName {
		// No-op, this container does not belong to this node.
		return nil
	}

	profiles, err := d.cluster.ProfilesGet(args.Project, args.Profiles)
	if err != nil {
		return err
	}
	for i, profileName := range args.Profiles {
		if profileName == name {
			// Use the old config and devices.
			profiles[i].Config = old.Config
			profiles[i].Devices = old.Devices
			break
		}
	}

	c := containerLXCInstantiate(d.State(), args)

	c.expandConfig(profiles)
	c.expandDevices(profiles)

	return c.Update(db.ContainerArgs{
		Architecture: c.Architecture(),
		Config:       c.LocalConfig(),
		Description:  c.Description(),
		Devices:      c.LocalDevices(),
		Ephemeral:    c.IsEphemeral(),
		Profiles:     c.Profiles(),
		Project:      c.Project(),
	}, true)
}

// Query the db for information about containers associated with the given
// profile.
func getProfileContainersInfo(cluster *db.Cluster, project, profile string) ([]db.ContainerArgs, error) {
	// Query the db for information about containers associated with the
	// given profile.
	names, err := cluster.ProfileContainersGet(project, profile)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query containers with profile '%s'", profile)
	}
	containers := make([]db.ContainerArgs, len(names))
	for i, name := range names {
		var container *db.Container
		err := cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			container, err = tx.ContainerGet(project, name)
			return err
		})
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to fetch container '%s'", name)
		}
		containers[i] = db.ContainerToArgs(container)
	}

	return containers, nil
}
