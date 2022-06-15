package main

import (
	"context"
	"fmt"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func doProfileUpdate(d *Daemon, projectName string, name string, id int64, profile *api.Profile, req api.ProfilePut) error {
	// Check project limits.
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowProfileUpdate(tx, projectName, name, req)
	})
	if err != nil {
		return err
	}

	// Quick checks.
	err = instance.ValidConfig(d.os, req.Config, false, instancetype.Any)
	if err != nil {
		return err
	}

	// Profiles can be applied to any instance type, so just use instancetype.Any type for validation so that
	// instance type specific validation checks are not performed.
	err = instance.ValidDevices(d.State(), projectName, instancetype.Any, deviceConfig.NewDevices(req.Devices), false)
	if err != nil {
		return err
	}

	insts, err := getProfileInstancesInfo(d.db.Cluster, projectName, name)
	if err != nil {
		return fmt.Errorf("Failed to query instances associated with profile %q: %w", name, err)
	}

	// Check if the root disk device's pool is supposed to be changed or removed and prevent that if there are
	// instances using that root disk device.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := shared.GetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if len(insts) > 0 && oldProfileRootDiskDevice["pool"] != "" && newProfileRootDiskDevice["pool"] == "" || (oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {
		// Check for instances using the device.
		for _, inst := range insts {
			// Check if the device is locally overridden.
			k, v, _ := shared.GetRootDiskDevice(inst.Devices.CloneNative())
			if k != "" && v["pool"] != "" {
				continue
			}

			// Check what profile the device comes from by working backwards along the profiles list.
			for i := len(inst.Profiles) - 1; i >= 0; i-- {
				_, profile, err := d.db.Cluster.GetProfile(projectName, inst.Profiles[i].Name)
				if err != nil {
					return err
				}

				// Check if we find a match for the device.
				_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
				if ok {
					// Found the profile.
					if inst.Profiles[i].Name == name {
						// If it's the current profile, then we can't modify that root device.
						return fmt.Errorf("At least one instance relies on this profile's root disk device")
					}

					// If it's not, then move on to the next instance.
					break
				}
			}
		}
	}

	// Update the database.
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := cluster.APIToDevices(req.Devices)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfile(ctx, tx.Tx(), projectName, name, cluster.Profile{
			Project:     projectName,
			Name:        name,
			Description: req.Description,
		})
		if err != nil {
			return err
		}

		id, err := cluster.GetProfileID(ctx, tx.Tx(), projectName, name)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfileConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices)
		if err != nil {
			return nil
		}

		newProfiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, []string{name})
		if err != nil {
			return err
		}

		if len(newProfiles) != 1 {
			return fmt.Errorf("Failed to find profile %q in project %q", name, projectName)
		}

		apiProfile, err := newProfiles[0].ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Update the profile on our current list of instances.
		for i := range insts {
			for j, profile := range insts[i].Profiles {
				if profile.Name == apiProfile.Name {
					insts[i].Profiles[j] = *apiProfile
					break
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Update all the instances on this node using the profile. Must be done after db.TxCommit due to DB lock.
	nodeName := ""
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		nodeName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to query local cluster member name: %w", err)
	}

	failures := map[*db.InstanceArgs]error{}
	for _, it := range insts {
		inst := it // Local var for instance pointer.
		err := doProfileUpdateInstance(d, name, profile.ProfilePut, nodeName, inst)
		if err != nil {
			failures[&inst] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following instances failed to update (profile change still saved):\n"
		for inst, err := range failures {
			msg += fmt.Sprintf(" - Project: %s, Instance: %s: %v\n", inst.Project, inst.Name, err)
		}

		return fmt.Errorf("%s", msg)
	}

	return nil
}

// Like doProfileUpdate but does not update the database, since it was already
// updated by doProfileUpdate itself, called on the notifying node.
func doProfileUpdateCluster(d *Daemon, projectName string, name string, old api.ProfilePut) error {
	nodeName := ""
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		nodeName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to query local cluster member name: %w", err)
	}

	insts, err := getProfileInstancesInfo(d.db.Cluster, projectName, name)
	if err != nil {
		return fmt.Errorf("Failed to query instances associated with profile %q: %w", name, err)
	}

	failures := map[*db.InstanceArgs]error{}
	for _, it := range insts {
		inst := it // Local var for instance pointer.
		err := doProfileUpdateInstance(d, name, old, nodeName, inst)
		if err != nil {
			failures[&inst] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following instances failed to update (profile change still saved):\n"
		for inst, err := range failures {
			msg += fmt.Sprintf(" - Project: %s, Instance: %s: %v\n", inst.Project, inst.Name, err)
		}

		return fmt.Errorf("%s", msg)
	}

	return nil
}

// Profile update of a single instance.
func doProfileUpdateInstance(d *Daemon, name string, old api.ProfilePut, nodeName string, args db.InstanceArgs) error {
	if args.Node != "" && args.Node != nodeName {
		// No-op, this instance does not belong to this node.
		return nil
	}

	profileNames := make([]string, 0, len(args.Profiles))
	for _, profile := range args.Profiles {
		profileNames = append(profileNames, profile.Name)
	}

	profiles, err := d.db.Cluster.GetProfiles(args.Project, profileNames)
	if err != nil {
		return err
	}

	for i, profile := range args.Profiles {
		if profile.Name == name {
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
		Profiles:     inst.Profiles(), // List of profile names to load from DB.
		Project:      inst.Project(),
		Type:         inst.Type(),
		Snapshot:     inst.IsSnapshot(),
	}, true)
}

// Query the db for information about instances associated with the given profile.
func getProfileInstancesInfo(dbCluster *db.Cluster, projectName string, profileName string) ([]db.InstanceArgs, error) {
	// Query the db for information about instances associated with the given profile.
	projectInstNames, err := dbCluster.GetInstancesWithProfile(projectName, profileName)
	if err != nil {
		return nil, fmt.Errorf("Failed to query instances with profile %q: %w", profileName, err)
	}

	instances := []db.InstanceArgs{}
	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		for instProject, instNames := range projectInstNames {
			for _, instName := range instNames {
				inst, err := cluster.GetInstance(ctx, tx.Tx(), instProject, instName)
				if err != nil {
					return err
				}

				args, err := db.InstanceToArgs(ctx, tx.Tx(), inst)
				if err != nil {
					return err
				}

				instances = append(instances, *args)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch instances: %w", err)
	}

	return instances, nil
}
