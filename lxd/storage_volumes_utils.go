package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
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

func storagePoolVolumeTypeNameToType(volumeTypeName string) (int, error) {
	switch volumeTypeName {
	case storagePoolVolumeTypeNameContainer:
		return storagePoolVolumeTypeContainer, nil
	case storagePoolVolumeTypeNameImage:
		return storagePoolVolumeTypeImage, nil
	case storagePoolVolumeTypeNameCustom:
		return storagePoolVolumeTypeCustom, nil
	}

	return -1, fmt.Errorf("invalid storage volume type name")
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

func storagePoolVolumeTypeToName(volumeType int) (string, error) {
	return db.StoragePoolVolumeTypeToName(volumeType)
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

func storagePoolVolumeRestore(state *state.State, poolName string, volumeName string, volumeType int, snapshotName string) error {
	s, err := storagePoolVolumeInit(state, "default", poolName,
		fmt.Sprintf("%s/%s", volumeName, snapshotName), volumeType)
	if err != nil {
		return err
	}

	snapshotWritable := s.GetStoragePoolVolumeWritable()
	snapshotWritable.Restore = snapshotName

	s, err = storagePoolVolumeInit(state, "default", poolName, volumeName, volumeType)
	if err != nil {
		return err
	}

	err = s.StoragePoolVolumeUpdate(&snapshotWritable, nil)
	if err != nil {
		return err
	}

	return nil
}

func storagePoolVolumeUpdate(state *state.State, poolName string, volumeName string, volumeType int, newDescription string, newConfig map[string]string) error {
	s, err := storagePoolVolumeInit(state, "default", poolName, volumeName, volumeType)
	if err != nil {
		return err
	}

	oldWritable := s.GetStoragePoolVolumeWritable()
	newWritable := oldWritable

	// Backup the current state
	oldDescription := oldWritable.Description
	oldConfig := map[string]string{}
	err = shared.DeepCopy(&oldWritable.Config, &oldConfig)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path. Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			s.SetStoragePoolVolumeWritable(&oldWritable)
		}
	}()

	// Diff the configurations
	changedConfig := []string{}
	userOnly := true
	for key := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Apply config changes if there are any
	if len(changedConfig) != 0 {
		newWritable.Description = newDescription
		newWritable.Config = newConfig

		// Update the storage pool
		if !userOnly {
			err = s.StoragePoolVolumeUpdate(&newWritable, changedConfig)
			if err != nil {
				return err
			}
		}

		// Apply the new configuration
		s.SetStoragePoolVolumeWritable(&newWritable)
	}

	// Unset idmap keys if volume is unmapped
	if shared.IsTrue(newConfig["security.unmapped"]) {
		delete(newConfig, "volatile.idmap.last")
		delete(newConfig, "volatile.idmap.next")
	}

	// Get the pool ID
	poolID, err := state.Cluster.StoragePoolGetID(poolName)
	if err != nil {
		return err
	}

	// Update the database if something changed
	if len(changedConfig) != 0 || newDescription != oldDescription {
		err = state.Cluster.StoragePoolVolumeUpdate(volumeName, volumeType, poolID, newDescription, newConfig)
		if err != nil {
			return err
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}

func storagePoolVolumeSnapshotUpdate(state *state.State, poolName string, volumeName string, volumeType int, newDescription string) error {
	s, err := storagePoolVolumeInit(state, "default", poolName, volumeName, volumeType)
	if err != nil {
		return err
	}

	oldWritable := s.GetStoragePoolVolumeWritable()
	oldDescription := oldWritable.Description

	poolID, err := state.Cluster.StoragePoolGetID(poolName)
	if err != nil {
		return err
	}

	// Update the database if something changed
	if newDescription != oldDescription {
		return state.Cluster.StoragePoolVolumeUpdate(volumeName, volumeType, poolID, newDescription, oldWritable.Config)
	}

	return nil
}

func storagePoolVolumeUsedByContainersGet(s *state.State, project, volumeName string,
	volumeTypeName string) ([]string, error) {
	cts, err := containerLoadByProject(s, project)
	if err != nil {
		return []string{}, err
	}

	ctsUsingVolume := []string{}
	volumeNameWithType := fmt.Sprintf("%s/%s", volumeTypeName, volumeName)
	for _, c := range cts {
		for _, dev := range c.LocalDevices() {
			if dev["type"] != "disk" {
				continue
			}

			// Make sure that we don't compare against stuff like
			// "container////bla" but only against "container/bla".
			cleanSource := filepath.Clean(dev["source"])
			if cleanSource == volumeName || cleanSource == volumeNameWithType {
				ctsUsingVolume = append(ctsUsingVolume, c.Name())
			}
		}
	}

	return ctsUsingVolume, nil
}

func storagePoolVolumeUpdateUsers(d *Daemon, oldPoolName string,
	oldVolumeName string, newPoolName string, newVolumeName string) error {

	s := d.State()
	// update all containers
	cts, err := containerLoadAll(s)
	if err != nil {
		return err
	}

	for _, c := range cts {
		devices := c.LocalDevices()
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

		args := db.ContainerArgs{
			Architecture: c.Architecture(),
			Description:  c.Description(),
			Config:       c.LocalConfig(),
			Devices:      devices,
			Ephemeral:    c.IsEphemeral(),
			Profiles:     c.Profiles(),
			Project:      c.Project(),
		}

		err = c.Update(args, false)
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
	cts, err := containerLoadAll(s)
	if err != nil {
		return []string{}, err
	}

	ctsUsingVolume := []string{}
	volumeNameWithType := fmt.Sprintf("%s/%s", volumeTypeName, volumeName)
	for _, c := range cts {
		if runningOnly && !c.IsRunning() {
			continue
		}

		for _, dev := range c.ExpandedDevices() {
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
				ctsUsingVolume = append(ctsUsingVolume, c.Name())
			}
		}
	}

	return ctsUsingVolume, nil
}

// volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
func storagePoolVolumeUsedByGet(s *state.State, project, volumeName string, volumeTypeName string) ([]string, error) {
	// Handle container volumes
	if volumeTypeName == "container" {
		cName, sName, snap := containerGetParentAndSnapshotName(volumeName)

		if snap {
			return []string{fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, cName, sName)}, nil
		}

		return []string{fmt.Sprintf("/%s/containers/%s", version.APIVersion, cName)}, nil
	}

	// Handle image volumes
	if volumeTypeName == "image" {
		return []string{fmt.Sprintf("/%s/images/%s", version.APIVersion, volumeName)}, nil
	}

	// Look for containers using this volume
	ctsUsingVolume, err := storagePoolVolumeUsedByContainersGet(s,
		project, volumeName, volumeTypeName)
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

func storagePoolVolumeDBCreate(s *state.State, poolName string, volumeName, volumeDescription string, volumeTypeName string, snapshot bool, volumeConfig map[string]string) error {
	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return err
	}

	// Load storage pool the volume will be attached to.
	poolID, poolStruct, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	// Check that a storage volume of the same storage volume type does not
	// already exist.
	volumeID, _ := s.Cluster.StoragePoolNodeVolumeGetTypeID(volumeName, volumeType, poolID)
	if volumeID > 0 {
		return fmt.Errorf("a storage volume of type %s does already exist", volumeTypeName)
	}

	// Make sure that we don't pass a nil to the next function.
	if volumeConfig == nil {
		volumeConfig = map[string]string{}
	}

	// Validate the requested storage volume configuration.
	err = storageVolumeValidateConfig(poolName, volumeConfig, poolStruct)
	if err != nil {
		return err
	}

	err = storageVolumeFillDefault(poolName, volumeConfig, poolStruct)
	if err != nil {
		return err
	}

	// Create the database entry for the storage volume.
	_, err = s.Cluster.StoragePoolVolumeCreate("default", volumeName, volumeDescription, volumeType, snapshot, poolID, volumeConfig)
	if err != nil {
		return fmt.Errorf("Error inserting %s of type %s into database: %s", poolName, volumeTypeName, err)
	}

	return nil
}

func storagePoolVolumeDBCreateInternal(state *state.State, poolName string, vol *api.StorageVolumesPost) (storage, error) {
	volumeName := vol.Name
	volumeDescription := vol.Description
	volumeTypeName := vol.Type
	volumeConfig := vol.Config

	if vol.Source.Name != "" {
		// Initialize instance of new pool to translate properties
		// between storage drivers.
		s, err := storagePoolInit(state, poolName)
		if err != nil {
			return nil, err
		}

		driver := s.GetStorageTypeName()
		newConfig, err := storageVolumePropertiesTranslate(vol.Config, driver)
		if err != nil {
			return nil, err
		}

		vol.Config = newConfig
		volumeConfig = newConfig
	}

	// Create database entry for new storage volume.
	err := storagePoolVolumeDBCreate(state, poolName, volumeName, volumeDescription, volumeTypeName, false, volumeConfig)
	if err != nil {
		return nil, err
	}

	// Convert the volume type name to our internal integer representation.
	poolID, err := state.Cluster.StoragePoolGetID(poolName)
	if err != nil {
		return nil, err
	}

	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		state.Cluster.StoragePoolVolumeDelete("default", volumeName, volumeType, poolID)
		return nil, err
	}

	// Initialize new storage volume on the target storage pool.
	s, err := storagePoolVolumeInit(state, "default", poolName, volumeName, volumeType)
	if err != nil {
		state.Cluster.StoragePoolVolumeDelete("default", volumeName, volumeType, poolID)
		return nil, err
	}

	return s, nil
}

func storagePoolVolumeCreateInternal(state *state.State, poolName string, vol *api.StorageVolumesPost) error {
	s, err := storagePoolVolumeDBCreateInternal(state, poolName, vol)
	if err != nil {
		return err
	}

	if vol.Source.Name == "" {
		err = s.StoragePoolVolumeCreate()
	} else {
		err = s.StoragePoolVolumeCopy(&vol.Source)
	}
	if err != nil {
		poolID, _, _ := s.GetContainerPoolInfo()

		volumeType, err1 := storagePoolVolumeTypeNameToType(vol.Type)
		if err1 == nil {
			state.Cluster.StoragePoolVolumeDelete("default", vol.Name, volumeType, poolID)
		}

		return err
	}

	return nil
}

func storagePoolVolumeSnapshotCreateInternal(state *state.State, poolName string, vol *api.StorageVolumesPost) error {
	// Get poolID of source pool
	poolID, err := state.Cluster.StoragePoolGetID(vol.Source.Pool)
	if err != nil {
		return err
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(vol.Type)
	if err != nil {
		return err
	}

	_, snapshot, err := state.Cluster.StoragePoolNodeVolumeGetType(vol.Source.Name, volumeType, poolID)
	if err != nil {
		return err
	}

	writable := snapshot.Writable()

	dbArgs := &db.StorageVolumeArgs{
		Name:        vol.Name,
		PoolName:    poolName,
		TypeName:    vol.Type,
		Snapshot:    true,
		Config:      writable.Config,
		Description: writable.Description,
	}

	s, err := storagePoolVolumeSnapshotDBCreateInternal(state, dbArgs)
	if err != nil {
		return err
	}

	if vol.Source.Name == "" {
		err = s.StoragePoolVolumeCreate()
	} else {
		err = s.StoragePoolVolumeCopy(&vol.Source)
	}
	if err != nil {
		poolID, _, _ := s.GetContainerPoolInfo()

		volumeType, err1 := storagePoolVolumeTypeNameToType(vol.Type)
		if err1 == nil {
			state.Cluster.StoragePoolVolumeDelete("default", vol.Name, volumeType, poolID)
		}

		return err
	}

	return nil
}

func storagePoolVolumeSnapshotDBCreateInternal(state *state.State, dbArgs *db.StorageVolumeArgs) (storage, error) {
	// Create database entry for new storage volume.
	err := storagePoolVolumeDBCreate(state, dbArgs.PoolName, dbArgs.Name, dbArgs.Description, dbArgs.TypeName, true, dbArgs.Config)
	if err != nil {
		return nil, err
	}

	// Convert the volume type name to our internal integer representation.
	poolID, err := state.Cluster.StoragePoolGetID(dbArgs.PoolName)
	if err != nil {
		return nil, err
	}

	volumeType, err := storagePoolVolumeTypeNameToType(dbArgs.TypeName)
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
