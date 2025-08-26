package main

import (
	"context"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var supportedVolumeTypes = []cluster.StoragePoolVolumeType{cluster.StoragePoolVolumeTypeContainer, cluster.StoragePoolVolumeTypeVM, cluster.StoragePoolVolumeTypeCustom, cluster.StoragePoolVolumeTypeImage}

func storagePoolVolumeUpdateUsers(ctx context.Context, s *state.State, projectName string, oldPoolName string, oldVol *api.StorageVolume, newPoolName string, newVol *api.StorageVolume) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	var instances []instance.Instance
	var instancesOldArgs []db.InstanceArgs
	var instancesNewDevices []config.Devices

	// Get all instances that are using the volume with a local (non-expanded) device.
	err := storagePools.VolumeUsedByInstanceDevices(s, oldPoolName, projectName, oldVol, false, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		inst, err := instance.Load(s, dbInst, project)
		if err != nil {
			return err
		}

		localDevices := inst.LocalDevices()
		newDevices := localDevices.Clone()

		for _, devName := range usedByDevices {
			_, exists := newDevices[devName]
			if exists {
				newDevices[devName]["pool"] = newPoolName
				newDevices[devName]["source"] = newVol.Name
			}
		}

		instances = append(instances, inst)
		instancesOldArgs = append(instancesOldArgs, dbInst)
		instancesNewDevices = append(instancesNewDevices, newDevices)

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Iterate over all instances and update their devices.
	// Don't perform this within a transaction as the instance's Update will persist the updates to file.
	// Furthermore this allows requesting further information from the database down the line.
	for i, inst := range instances {
		args := db.InstanceArgs{
			Architecture: inst.Architecture(),
			Description:  inst.Description(),
			Config:       inst.LocalConfig(),
			Devices:      instancesNewDevices[i],
			Ephemeral:    inst.IsEphemeral(),
			Profiles:     inst.Profiles(),
			Project:      inst.Project().Name,
			Type:         inst.Type(),
			Snapshot:     inst.IsSnapshot(),
		}

		err = inst.Update(args, false)
		if err != nil {
			return nil, err
		}

		revert.Add(func() {
			err := inst.Update(instancesOldArgs[i], false)
			if err != nil {
				logger.Error("Failed to revert instance update", logger.Ctx{"project": instancesOldArgs[i].Project, "instance": instancesOldArgs[i].Name, "error": err})
			}
		})
	}

	// Update all profiles that are using the volume with a device.
	err = storagePools.VolumeUsedByProfileDevices(s, oldPoolName, projectName, oldVol, func(profileID int64, profile api.Profile, p api.Project, usedByDevices []string) error {
		newDevices := make(map[string]map[string]string, len(profile.Devices))

		for devName, dev := range profile.Devices {
			for key, val := range dev {
				_, exists := newDevices[devName]
				if !exists {
					newDevices[devName] = make(map[string]string, len(dev))
				}

				newDevices[devName][key] = val
			}

			if slices.Contains(usedByDevices, devName) {
				newDevices[devName]["pool"] = newPoolName
				newDevices[devName]["source"] = newVol.Name
			}
		}

		pUpdate := api.ProfilePut{
			Config:      profile.Config,
			Description: profile.Description,
			Devices:     newDevices,
		}

		err = doProfileUpdate(ctx, s, p, profile.Name, &profile, pUpdate)
		if err != nil {
			return err
		}

		revert.Add(func() {
			original := api.ProfilePut{
				Config:      profile.Config,
				Description: profile.Description,
				Devices:     profile.Devices,
			}

			err := doProfileUpdate(ctx, s, p, profile.Name, &profile, original)
			if err != nil {
				logger.Error("Failed reverting profile update", logger.Ctx{"project": p.Name, "profile": profile.Name, "error": err})
			}
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// storagePoolVolumeUsedByGet returns a list of URL resources that use the volume.
func storagePoolVolumeUsedByGet(s *state.State, requestProjectName string, vol *db.StorageVolume) ([]string, error) {
	if vol.Type == cluster.StoragePoolVolumeTypeNameContainer {
		volName, snapName, isSnap := api.GetParentAndSnapshotName(vol.Name)
		if isSnap {
			return []string{api.NewURL().Path(version.APIVersion, "instances", volName, "snapshots", snapName).Project(vol.Project).String()}, nil
		}

		return []string{api.NewURL().Path(version.APIVersion, "instances", volName).Project(vol.Project).String()}, nil
	}

	// Handle image volumes.
	if vol.Type == cluster.StoragePoolVolumeTypeNameImage {
		return []string{api.NewURL().Path(version.APIVersion, "images", vol.Name).Project(requestProjectName).Target(vol.Location).String()}, nil
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(s, vol.Pool, vol.Name)
	if err != nil {
		return []string{}, err
	}

	if used {
		return []string{api.NewURL().Path(version.APIVersion).String()}, nil
	}

	// Look for instances using this volume.
	volumeUsedBy := []string{}

	// Pass false to expandDevices, as we only want to see instances directly using a volume, rather than their
	// profiles using a volume.
	err = storagePools.VolumeUsedByInstanceDevices(s, vol.Pool, vol.Project, &vol.StorageVolume, false, func(inst db.InstanceArgs, _ api.Project, _ []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "instances", inst.Name).Project(inst.Project).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	err = storagePools.VolumeUsedByProfileDevices(s, vol.Pool, requestProjectName, &vol.StorageVolume, func(_ int64, profile api.Profile, p api.Project, _ []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(p.Name).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	// Handle instance volumes.
	if vol.Type == cluster.StoragePoolVolumeTypeNameVM {
		volName, snapName, isSnap := api.GetParentAndSnapshotName(vol.Name)
		if isSnap {
			return []string{api.NewURL().Path(version.APIVersion, "instances", volName, "snapshots", snapName).Project(vol.Project).String()}, nil
		}

		// VolumeUsedByInstanceDevices will find virtual-machine/container volumes
		// when they are a root disk device in an instance's unexpanded devices,
		// but not in a profile's devices.
		// Since every virtual-machine/container volume is always in use by its
		// corresponding instance, this ensures that it is reported.
		instancePath := api.NewURL().Path(version.APIVersion, "instances", volName).Project(vol.Project).String()
		if !slices.Contains(volumeUsedBy, instancePath) {
			volumeUsedBy = append(volumeUsedBy, instancePath)
		}
	}

	return volumeUsedBy, nil
}

func storagePoolVolumeBackupLoadByName(ctx context.Context, s *state.State, projectName, poolName, backupName string) (*backup.VolumeBackup, error) {
	var b db.StoragePoolVolumeBackup

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		b, err = tx.GetStoragePoolVolumeBackup(ctx, projectName, poolName, backupName)
		return err
	})
	if err != nil {
		return nil, err
	}

	volumeName := strings.Split(backupName, "/")[0]
	backup := backup.NewVolumeBackup(s, projectName, poolName, volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)

	return backup, nil
}
