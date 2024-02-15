package main

import (
	"strings"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

var supportedVolumeTypes = []int{cluster.StoragePoolVolumeTypeContainer, cluster.StoragePoolVolumeTypeVM, cluster.StoragePoolVolumeTypeCustom, cluster.StoragePoolVolumeTypeImage}

func storagePoolVolumeUpdateUsers(s *state.State, projectName string, oldPoolName string, oldVol *api.StorageVolume, newPoolName string, newVol *api.StorageVolume) error {
	// Update all instances that are using the volume with a local (non-expanded) device.
	err := storagePools.VolumeUsedByInstanceDevices(s, oldPoolName, projectName, oldVol, false, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		inst, err := instance.Load(s, dbInst, project)
		if err != nil {
			return err
		}

		localDevices := inst.LocalDevices()
		for _, devName := range usedByDevices {
			_, exists := localDevices[devName]
			if exists {
				localDevices[devName]["pool"] = newPoolName
				localDevices[devName]["source"] = newVol.Name
			}
		}

		args := db.InstanceArgs{
			Architecture: inst.Architecture(),
			Description:  inst.Description(),
			Config:       inst.LocalConfig(),
			Devices:      localDevices,
			Ephemeral:    inst.IsEphemeral(),
			Profiles:     inst.Profiles(),
			Project:      inst.Project().Name,
			Type:         inst.Type(),
			Snapshot:     inst.IsSnapshot(),
		}

		err = inst.Update(args, false)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Update all profiles that are using the volume with a device.
	err = storagePools.VolumeUsedByProfileDevices(s, oldPoolName, projectName, oldVol, func(profileID int64, profile api.Profile, p api.Project, usedByDevices []string) error {
		for name, dev := range profile.Devices {
			if shared.ValueInSlice(name, usedByDevices) {
				dev["pool"] = newPoolName
				dev["source"] = newVol.Name
			}
		}

		pUpdate := api.ProfilePut{}
		pUpdate.Config = profile.Config
		pUpdate.Description = profile.Description
		pUpdate.Devices = profile.Devices
		err = doProfileUpdate(s, p, profile.Name, profileID, &profile, pUpdate)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// storagePoolVolumeUsedByGet returns a list of URL resources that use the volume.
func storagePoolVolumeUsedByGet(s *state.State, requestProjectName string, poolName string, vol *db.StorageVolume) ([]string, error) {
	// Handle instance volumes.
	if vol.Type == cluster.StoragePoolVolumeTypeNameContainer || vol.Type == cluster.StoragePoolVolumeTypeNameVM {
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
	used, err := storagePools.VolumeUsedByDaemon(s, poolName, vol.Name)
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
	err = storagePools.VolumeUsedByInstanceDevices(s, poolName, vol.Project, &vol.StorageVolume, false, func(inst db.InstanceArgs, p api.Project, usedByDevices []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "instances", inst.Name).Project(inst.Project).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	err = storagePools.VolumeUsedByProfileDevices(s, poolName, requestProjectName, &vol.StorageVolume, func(profileID int64, profile api.Profile, p api.Project, usedByDevices []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(p.Name).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	return volumeUsedBy, nil
}

func storagePoolVolumeBackupLoadByName(s *state.State, projectName, poolName, backupName string) (*backup.VolumeBackup, error) {
	b, err := s.DB.Cluster.GetStoragePoolVolumeBackup(projectName, poolName, backupName)
	if err != nil {
		return nil, err
	}

	volumeName := strings.Split(backupName, "/")[0]
	backup := backup.NewVolumeBackup(s, projectName, poolName, volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)

	return backup, nil
}
