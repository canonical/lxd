package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	storagePoolVolumeAPIEndpointContainers string = "containers"
	storagePoolVolumeAPIEndpointVMs        string = "virtual-machines"
	storagePoolVolumeAPIEndpointImages     string = "images"
	storagePoolVolumeAPIEndpointCustom     string = "custom"
)

var supportedVolumeTypes = []int{db.StoragePoolVolumeTypeContainer, db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom, db.StoragePoolVolumeTypeImage}
var supportedVolumeTypesInstances = []int{db.StoragePoolVolumeTypeContainer, db.StoragePoolVolumeTypeVM}

func storagePoolVolumeTypeNameToAPIEndpoint(volumeTypeName string) (string, error) {
	switch volumeTypeName {
	case db.StoragePoolVolumeTypeNameContainer:
		return storagePoolVolumeAPIEndpointContainers, nil
	case db.StoragePoolVolumeTypeNameVM:
		return storagePoolVolumeAPIEndpointVMs, nil
	case db.StoragePoolVolumeTypeNameImage:
		return storagePoolVolumeAPIEndpointImages, nil
	case db.StoragePoolVolumeTypeNameCustom:
		return storagePoolVolumeAPIEndpointCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type name")
}

func storagePoolVolumeTypeToAPIEndpoint(volumeType int) (string, error) {
	switch volumeType {
	case db.StoragePoolVolumeTypeContainer:
		return storagePoolVolumeAPIEndpointContainers, nil
	case db.StoragePoolVolumeTypeVM:
		return storagePoolVolumeAPIEndpointVMs, nil
	case db.StoragePoolVolumeTypeImage:
		return storagePoolVolumeAPIEndpointImages, nil
	case db.StoragePoolVolumeTypeCustom:
		return storagePoolVolumeAPIEndpointCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type")
}

func storagePoolVolumeUpdateUsers(d *Daemon, projectName string, oldPoolName string, oldVolumeName string, newPoolName string, newVolumeName string) error {
	s := d.State()
	// update all instances
	insts, err := instance.LoadByProject(s, projectName)
	if err != nil {
		return err
	}

	for _, inst := range insts {
		devices := inst.LocalDevices()
		found := false
		for k := range devices {
			if devices[k]["type"] != "disk" {
				continue
			}

			// Can't be a storage volume.
			if filepath.IsAbs(devices[k]["source"]) {
				continue
			}

			if filepath.Clean(devices[k]["pool"]) != oldPoolName {
				continue
			}

			dir, file := filepath.Split(devices[k]["source"])
			dir = filepath.Clean(dir)
			if dir != db.StoragePoolVolumeTypeNameCustom {
				continue
			}

			file = filepath.Clean(file)
			if file != oldVolumeName {
				continue
			}

			// found entry
			found = true

			if oldPoolName != newPoolName {
				devices[k]["pool"] = newPoolName
			}

			if oldVolumeName != newVolumeName {
				newSource := newVolumeName
				if dir != "" {
					newSource = fmt.Sprintf("%s/%s", db.StoragePoolVolumeTypeNameCustom, newVolumeName)
				}
				devices[k]["source"] = newSource
			}
		}

		if !found {
			continue
		}

		args := db.InstanceArgs{
			Architecture: inst.Architecture(),
			Description:  inst.Description(),
			Config:       inst.LocalConfig(),
			Devices:      devices,
			Ephemeral:    inst.IsEphemeral(),
			Profiles:     inst.Profiles(),
			Project:      inst.Project(),
			Type:         inst.Type(),
			Snapshot:     inst.IsSnapshot(),
		}

		err = inst.Update(args, false)
		if err != nil {
			return err
		}
	}

	// update all profiles
	profiles, err := s.Cluster.GetProfileNames(project.Default)
	if err != nil {
		return err
	}

	for _, pName := range profiles {
		id, profile, err := s.Cluster.GetProfile(project.Default, pName)
		if err != nil {
			return err
		}

		found := false
		for k := range profile.Devices {
			if profile.Devices[k]["type"] != "disk" {
				continue
			}

			// Can't be a storage volume.
			if filepath.IsAbs(profile.Devices[k]["source"]) {
				continue
			}

			if filepath.Clean(profile.Devices[k]["pool"]) != oldPoolName {
				continue
			}

			dir, file := filepath.Split(profile.Devices[k]["source"])
			dir = filepath.Clean(dir)
			if dir != db.StoragePoolVolumeTypeNameCustom {
				continue
			}

			file = filepath.Clean(file)
			if file != oldVolumeName {
				continue
			}

			// found entry
			found = true

			if oldPoolName != newPoolName {
				profile.Devices[k]["pool"] = newPoolName
			}

			if oldVolumeName != newVolumeName {
				newSource := newVolumeName
				if dir != "" {
					newSource = fmt.Sprintf("%s/%s", db.StoragePoolVolumeTypeNameCustom, newVolumeName)
				}
				profile.Devices[k]["source"] = newSource
			}
		}

		if !found {
			continue
		}

		pUpdate := api.ProfilePut{}
		pUpdate.Config = profile.Config
		pUpdate.Description = profile.Description
		pUpdate.Devices = profile.Devices
		err = doProfileUpdate(d, project.Default, pName, id, profile, pUpdate)
		if err != nil {
			return err
		}
	}

	return nil
}

// volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
func storagePoolVolumeUsedByGet(s *state.State, projectName string, poolName string, volumeName string, volumeTypeName string) ([]string, error) {
	// Handle instance volumes.
	if volumeTypeName == "container" || volumeTypeName == "virtual-machine" {
		cName, sName, snap := shared.InstanceGetParentAndSnapshotName(volumeName)
		if snap {
			if projectName == project.Default {
				return []string{fmt.Sprintf("/%s/instances/%s/snapshots/%s", version.APIVersion, cName, sName)}, nil
			} else {
				return []string{fmt.Sprintf("/%s/instances/%s/snapshots/%s?project=%s", version.APIVersion, cName, sName, projectName)}, nil
			}
		}

		if projectName == project.Default {
			return []string{fmt.Sprintf("/%s/instances/%s", version.APIVersion, cName)}, nil
		} else {
			return []string{fmt.Sprintf("/%s/instances/%s?project=%s", version.APIVersion, cName, projectName)}, nil
		}
	}

	// Handle image volumes.
	if volumeTypeName == "image" {
		if projectName == project.Default {
			return []string{fmt.Sprintf("/%s/images/%s", version.APIVersion, volumeName)}, nil
		} else {
			return []string{fmt.Sprintf("/%s/images/%s?project=%s", version.APIVersion, volumeName, projectName)}, nil
		}
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(s, poolName, volumeName)
	if err != nil {
		return []string{}, err
	}

	if used {
		return []string{fmt.Sprintf("/%s", version.APIVersion)}, nil
	}

	// Look for instances using this volume.
	volumeUsedBy := []string{}

	instancesUsingVolume := []*db.Instance{}

	// Pass false to expandDevices, as we only want to see instances directly using a volume, rather than their
	// profiles using a volume.
	err = storagePools.VolumeUsedByInstances(s, poolName, projectName, volumeName, volumeTypeName, false, func(inst db.Instance, project api.Project, profiles []api.Profile) error {
		instancesUsingVolume = append(instancesUsingVolume, &inst)
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	for _, inst := range instancesUsingVolume {
		if inst.Project == project.Default {
			volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/instances/%s", version.APIVersion, inst.Name))
		} else {
			volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/instances/%s?project=%s", version.APIVersion, inst.Name, inst.Project))
		}
	}

	// Look for profiles using this volume.
	profiles, err := profilesUsingPoolVolumeGetNames(s.Cluster, volumeName, volumeTypeName)
	if err != nil {
		return []string{}, err
	}

	for _, pName := range profiles {
		if projectName == project.Default {
			volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, pName))
		} else {
			volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/profiles/%s?project=%s", version.APIVersion, pName, projectName))
		}
	}

	return volumeUsedBy, nil
}

func profilesUsingPoolVolumeGetNames(db *db.Cluster, volumeName string, volumeType string) ([]string, error) {
	usedBy := []string{}

	profiles, err := db.GetProfileNames(project.Default)
	if err != nil {
		return usedBy, err
	}

	for _, pName := range profiles {
		_, profile, err := db.GetProfile(project.Default, pName)
		if err != nil {
			return usedBy, err
		}

		volumeNameWithType := fmt.Sprintf("%s/%s", volumeType, volumeName)
		for _, v := range profile.Devices {
			if v["type"] != "disk" {
				continue
			}

			// Can't be a storage volume.
			if filepath.IsAbs(v["source"]) {
				continue
			}

			// Make sure that we don't compare against stuff
			// like "container////bla" but only against
			// "container/bla".
			cleanSource := filepath.Clean(v["source"])
			if cleanSource == volumeName || cleanSource == volumeNameWithType {
				usedBy = append(usedBy, pName)
			}
		}
	}

	return usedBy, nil
}

func storagePoolVolumeBackupLoadByName(s *state.State, projectName, poolName, backupName string) (*backup.VolumeBackup, error) {
	b, err := s.Cluster.GetStoragePoolVolumeBackup(projectName, poolName, backupName)
	if err != nil {
		return nil, err
	}

	volumeName := strings.Split(backupName, "/")[0]
	backup := backup.NewVolumeBackup(s, projectName, poolName, volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)

	return backup, nil
}
