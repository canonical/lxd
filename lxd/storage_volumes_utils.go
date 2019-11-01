package main

import (
	"fmt"
	"path/filepath"

	"github.com/lxc/lxd/lxd/db"
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
	storagePoolVolumeTypeImage     = db.StoragePoolVolumeTypeImage
	storagePoolVolumeTypeCustom    = db.StoragePoolVolumeTypeCustom
)

const (
	storagePoolVolumeTypeNameContainer = db.StoragePoolVolumeTypeNameContainer
	storagePoolVolumeTypeNameImage     = db.StoragePoolVolumeTypeNameImage
	storagePoolVolumeTypeNameCustom    = db.StoragePoolVolumeTypeNameCustom
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	storagePoolVolumeAPIEndpointContainers string = "containers"
	storagePoolVolumeAPIEndpointImages     string = "images"
	storagePoolVolumeAPIEndpointCustom     string = "custom"
)

var supportedVolumeTypesExceptImages = []int{storagePoolVolumeTypeContainer, storagePoolVolumeTypeCustom}
var supportedVolumeTypes = append(supportedVolumeTypesExceptImages, storagePoolVolumeTypeImage)

func init() {
	storagePools.VolumeUsedByInstancesWithProfiles = storagePoolVolumeUsedByRunningContainersWithProfilesGet
}

func storagePoolVolumeTypeNameToAPIEndpoint(volumeTypeName string) (string, error) {
	switch volumeTypeName {
	case storagePoolVolumeTypeNameContainer:
		return storagePoolVolumeAPIEndpointContainers, nil
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
	case storagePoolVolumeTypeImage:
		return storagePoolVolumeAPIEndpointImages, nil
	case storagePoolVolumeTypeCustom:
		return storagePoolVolumeAPIEndpointCustom, nil
	}

	return "", fmt.Errorf("invalid storage volume type")
}

func storagePoolVolumeUsedByContainersGet(s *state.State, project, poolName string, volumeName string) ([]string, error) {
	insts, err := instanceLoadByProject(s, project)
	if err != nil {
		return []string{}, err
	}

	ctsUsingVolume := []string{}
	for _, inst := range insts {
		for _, dev := range inst.LocalDevices() {
			if dev["type"] != "disk" {
				continue
			}

			if dev["pool"] == poolName && dev["source"] == volumeName {
				ctsUsingVolume = append(ctsUsingVolume, inst.Name())
				break
			}
		}
	}

	return ctsUsingVolume, nil
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

func storagePoolVolumeUsedByRunningContainersWithProfilesGet(s *state.State,
	poolName string, volumeName string, volumeTypeName string,
	runningOnly bool) ([]string, error) {
	insts, err := instanceLoadAll(s)
	if err != nil {
		return []string{}, err
	}

	ctsUsingVolume := []string{}
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
				ctsUsingVolume = append(ctsUsingVolume, inst.Name())
			}
		}
	}

	return ctsUsingVolume, nil
}

// volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
func storagePoolVolumeUsedByGet(s *state.State, project, poolName string, volumeName string, volumeTypeName string) ([]string, error) {
	// Handle container volumes
	if volumeTypeName == "container" {
		cName, sName, snap := shared.ContainerGetParentAndSnapshotName(volumeName)

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
	ctsUsingVolume, err := storagePoolVolumeUsedByContainersGet(s, project, poolName, volumeName)
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

func storagePoolVolumeSnapshotDBCreateInternal(state *state.State, dbArgs *db.StorageVolumeArgs) (storage, error) {
	// Create database entry for new storage volume.
	err := storagePools.VolumeDBCreate(state, dbArgs.PoolName, dbArgs.Name, dbArgs.Description, dbArgs.TypeName, true, dbArgs.Config)
	if err != nil {
		return nil, err
	}

	// Convert the volume type name to our internal integer representation.
	poolID, err := state.Cluster.StoragePoolGetID(dbArgs.PoolName)
	if err != nil {
		return nil, err
	}

	volumeType, err := storagePools.VolumeTypeNameToType(dbArgs.TypeName)
	if err != nil {
		state.Cluster.StoragePoolVolumeDelete("default", dbArgs.Name, volumeType, poolID)
		return nil, err
	}

	// Initialize new storage volume on the target storage pool.
	s, err := storagePoolVolumeInit(state, "default", dbArgs.PoolName, dbArgs.Name, volumeType)
	if err != nil {
		state.Cluster.StoragePoolVolumeDelete("default", dbArgs.Name, volumeType, poolID)
		return nil, err
	}

	return s, nil
}
