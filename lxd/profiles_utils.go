package main

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	projecthelpers "github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

func doProfileUpdate(d *Daemon, projectName string, name string, id int64, profile *api.Profile, req api.ProfilePut) error {
	// Check project limits.
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return projecthelpers.AllowProfileUpdate(tx, projectName, name, req)
	})
	if err != nil {
		return err
	}

	// Sanity checks
	err = instance.ValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return err
	}

	// At this point we don't know the instance type, so just use instancetype.Any type for validation.
	err = instance.ValidDevices(d.State(), d.cluster, projectName, instancetype.Any, deviceConfig.NewDevices(req.Devices), false)
	if err != nil {
		return err
	}

	containers, err := getProfileContainersInfo(d.cluster, projectName, name)
	if err != nil {
		return errors.Wrapf(err, "failed to query instances associated with profile '%s'", name)
	}

	// Check if the root device is supposed to be changed or removed.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := shared.GetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if len(containers) > 0 && oldProfileRootDiskDevice["pool"] != "" && newProfileRootDiskDevice["pool"] == "" || (oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {
		// Check for containers using the device
		for _, container := range containers {
			// Check if the device is locally overridden
			k, v, _ := shared.GetRootDiskDevice(container.Devices.CloneNative())
			if k != "" && v["pool"] != "" {
				continue
			}

			// Check what profile the device comes from
			profiles := container.Profiles
			for i := len(profiles) - 1; i >= 0; i-- {
				_, profile, err := d.cluster.GetProfile(projecthelpers.Default, profiles[i])
				if err != nil {
					return err
				}

				// Check if we find a match for the device
				_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
				if ok {
					// Found the profile
					if profiles[i] == name {
						// If it's the current profile, then we can't modify that root device
						return fmt.Errorf("At least one instance relies on this profile's root disk device")
					} else {
						// If it's not, then move on to the next container
						break
					}
				}
			}
		}
	}

	// Update the database
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.UpdateProfile(projectName, name, db.Profile{
			Project:     projectName,
			Name:        name,
			Description: req.Description,
			Config:      req.Config,
			Devices:     req.Devices,
		})
	})
	if err != nil {
		return err
	}

	// Update all the containers on this node using the profile. Must be
	// done after db.TxCommit due to DB lock.
	nodeName := ""
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		nodeName, err = tx.GetLocalNodeName()
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
		nodeName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return errors.Wrap(err, "Failed to query local node name")
	}

	containers, err := getProfileContainersInfo(d.cluster, project, name)
	if err != nil {
		return errors.Wrapf(err, "Failed to query instances associated with profile '%s'", name)
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
func doProfileUpdateContainer(d *Daemon, name string, old api.ProfilePut, nodeName string, args db.InstanceArgs) error {
	if args.Node != "" && args.Node != nodeName {
		// No-op, this container does not belong to this node.
		return nil
	}

	profiles, err := d.cluster.GetProfiles(args.Project, args.Profiles)
	if err != nil {
		return err
	}

	for i, profileName := range args.Profiles {
		if profileName == name {
			// Overwrite the new config from the database with the old config and devices.
			profiles[i].Config = old.Config
			profiles[i].Devices = old.Devices
			break
		}
	}

	// Load the instance using the old profile config.
	inst, err := instance.Load(d.State(), args, profiles)
	if err != nil {
		return err
	}

	// Update will internally load the new profile configs and detect the changes to apply.
	return inst.Update(db.InstanceArgs{
		Architecture: inst.Architecture(),
		Config:       inst.LocalConfig(),
		Description:  inst.Description(),
		Devices:      inst.LocalDevices(),
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     inst.Profiles(),
		Project:      inst.Project(),
		Type:         inst.Type(),
		Snapshot:     inst.IsSnapshot(),
	}, true)
}

// Query the db for information about containers associated with the given
// profile.
func getProfileContainersInfo(cluster *db.Cluster, project, profile string) ([]db.InstanceArgs, error) {
	// Query the db for information about containers associated with the
	// given profile.
	names, err := cluster.GetInstancesWithProfile(project, profile)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to query instances with profile '%s'", profile)
	}

	containers := []db.InstanceArgs{}
	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		for ctProject, ctNames := range names {
			for _, ctName := range ctNames {
				container, err := tx.GetInstance(ctProject, ctName)
				if err != nil {
					return err
				}

				containers = append(containers, db.InstanceToArgs(container))
			}
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to fetch instances")
	}

	return containers, nil
}
