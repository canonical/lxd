package main

import (
	"fmt"
	"path/filepath"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// XXX: backward compatible declarations, introduced when the db code was
//      extracted to its own package. We should eventually clean this up.
const (
	storagePoolVolumeTypeContainer = db.StoragePoolVolumeTypeContainer
	storagePoolVolumeTypeVM        = db.StoragePoolVolumeTypeVM
	storagePoolVolumeTypeImage     = db.StoragePoolVolumeTypeImage
	storagePoolVolumeTypeCustom    = db.StoragePoolVolumeTypeCustom
)

const (
	storagePoolVolumeTypeNameContainer = db.StoragePoolVolumeTypeNameContainer
	storagePoolVolumeTypeNameVM        = db.StoragePoolVolumeTypeNameVM
	storagePoolVolumeTypeNameImage     = db.StoragePoolVolumeTypeNameImage
	storagePoolVolumeTypeNameCustom    = db.StoragePoolVolumeTypeNameCustom
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

var supportedVolumeTypesExceptImages = []int{storagePoolVolumeTypeContainer, storagePoolVolumeTypeVM, storagePoolVolumeTypeCustom}
var supportedVolumeTypes = append(supportedVolumeTypesExceptImages, storagePoolVolumeTypeImage)

func init() {
	storagePools.VolumeUsedByInstancesWithProfiles = storagePoolVolumeUsedByRunningInstancesWithProfilesGet
}

func storagePoolVolumeTypeNameToAPIEndpoint(volumeTypeName string) (string, error) {
	switch volumeTypeName {
	case storagePoolVolumeTypeNameContainer:
		return storagePoolVolumeAPIEndpointContainers, nil
	case storagePoolVolumeTypeNameVM:
		return storagePoolVolumeAPIEndpointVMs, nil
	case storagePoolVolumeTypeNameImage:
		return storagePoolVolumeAPIEndpointImages, nil
	case storagePoolVolumeTypeNameCustom:
		return storagePoolVolumeAPIEndpointCustom, nil
	}

	return "", fmt.Errorf("invalid storage volume type name")
}

func storagePoolVolumeTypeToAPIEndpoint(volumeType int) (string, error) {
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		return storagePoolVolumeAPIEndpointContainers, nil
	case storagePoolVolumeTypeVM:
		return storagePoolVolumeAPIEndpointVMs, nil
	case storagePoolVolumeTypeImage:
		return storagePoolVolumeAPIEndpointImages, nil
	case storagePoolVolumeTypeCustom:
		return storagePoolVolumeAPIEndpointCustom, nil
	}

	return "", fmt.Errorf("invalid storage volume type")
}

func storagePoolVolumeUsedByInstancesGet(s *state.State, project, poolName string, volumeName string) ([]string, error) {
	insts, err := instance.LoadByProject(s, project)
	if err != nil {
		return []string{}, err
	}

	instUsingVolume := []string{}
	for _, inst := range insts {
		for _, dev := range inst.LocalDevices() {
			if dev["type"] != "disk" {
				continue
			}

			if dev["pool"] == poolName && dev["source"] == volumeName {
				instUsingVolume = append(instUsingVolume, inst.Name())
				break
			}
		}
	}

	return instUsingVolume, nil
}

func storagePoolVolumeUpdateUsers(d *Daemon, oldPoolName string,
	oldVolumeName string, newPoolName string, newVolumeName string) error {

	s := d.State()
	// update all instances
	insts, err := instanceLoadAll(s)
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
			if dir != storagePoolVolumeTypeNameCustom {
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
					newSource = fmt.Sprintf("%s/%s", storagePoolVolumeTypeNameCustom, newVolumeName)
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
	profiles, err := s.Cluster.Profiles("default")
	if err != nil {
		return err
	}

	for _, pName := range profiles {
		id, profile, err := s.Cluster.ProfileGet("default", pName)
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
			if dir != storagePoolVolumeTypeNameCustom {
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
					newSource = fmt.Sprintf("%s/%s", storagePoolVolumeTypeNameCustom, newVolumeName)
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
		err = doProfileUpdate(d, "default", pName, id, profile, pUpdate)
		if err != nil {
			return err
		}
	}

	return nil
}

func storagePoolVolumeUsedByRunningInstancesWithProfilesGet(s *state.State,
	poolName string, volumeName string, volumeTypeName string,
	runningOnly bool) ([]string, error) {
	insts, err := instanceLoadAll(s)
	if err != nil {
		return []string{}, err
	}

	instUsingVolume := []string{}
	volumeNameWithType := fmt.Sprintf("%s/%s", volumeTypeName, volumeName)
	for _, inst := range insts {
		if runningOnly && !inst.IsRunning() {
			continue
		}

		for _, dev := range inst.ExpandedDevices() {
			if dev["type"] != "disk" {
				continue
			}

			if dev["pool"] != poolName {
				continue
			}

			// Make sure that we don't compare against stuff like
			// "container////bla" but only against "container/bla".
			cleanSource := filepath.Clean(dev["source"])
			if cleanSource == volumeName || cleanSource == volumeNameWithType {
				instUsingVolume = append(instUsingVolume, inst.Name())
			}
		}
	}

	return instUsingVolume, nil
}

// volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
func storagePoolVolumeUsedByGet(s *state.State, project, poolName string, volumeName string, volumeTypeName string) ([]string, error) {
	// Handle container volumes
	if volumeTypeName == "container" {
		cName, sName, snap := shared.InstanceGetParentAndSnapshotName(volumeName)

		if snap {
			return []string{fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, cName, sName)}, nil
		}

		return []string{fmt.Sprintf("/%s/containers/%s", version.APIVersion, cName)}, nil
	}

	// Handle image volumes
	if volumeTypeName == "image" {
		return []string{fmt.Sprintf("/%s/images/%s", version.APIVersion, volumeName)}, nil
	}

	// Check if the daemon itself is using it
	used, err := daemonStorageUsed(s, poolName, volumeName)
	if err != nil {
		return []string{}, err
	}

	if used {
		return []string{fmt.Sprintf("/%s", version.APIVersion)}, nil
	}

	// Look for containers using this volume
	ctsUsingVolume, err := storagePoolVolumeUsedByInstancesGet(s, project, poolName, volumeName)
	if err != nil {
		return []string{}, err
	}

	volumeUsedBy := []string{}
	for _, ct := range ctsUsingVolume {
		volumeUsedBy = append(volumeUsedBy,
			fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
	}

	profiles, err := profilesUsingPoolVolumeGetNames(s.Cluster, volumeName, volumeTypeName)
	if err != nil {
		return []string{}, err
	}

	if len(volumeUsedBy) == 0 && len(profiles) == 0 {
		return []string{}, nil
	}

	for _, pName := range profiles {
		volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, pName))
	}

	return volumeUsedBy, nil
}

func profilesUsingPoolVolumeGetNames(db *db.Cluster, volumeName string, volumeType string) ([]string, error) {
	usedBy := []string{}

	profiles, err := db.Profiles("default")
	if err != nil {
		return usedBy, err
	}

	for _, pName := range profiles {
		_, profile, err := db.ProfileGet("default", pName)
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
