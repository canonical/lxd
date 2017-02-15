package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
)

const (
	storagePoolVolumeTypeContainer = iota
	storagePoolVolumeTypeImage
	storagePoolVolumeTypeCustom
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	storagePoolVolumeTypeNameContainer string = "container"
	storagePoolVolumeTypeNameImage     string = "image"
	storagePoolVolumeTypeNameCustom    string = "custom"
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	storagePoolVolumeApiEndpointContainers string = "containers"
	storagePoolVolumeApiEndpointImages     string = "images"
	storagePoolVolumeApiEndpointCustom     string = "custom"
)

var supportedVolumeTypes = []int{storagePoolVolumeTypeContainer, storagePoolVolumeTypeImage, storagePoolVolumeTypeCustom}

func storagePoolVolumeTypeNameToType(volumeTypeName string) (int, error) {
	switch volumeTypeName {
	case storagePoolVolumeTypeNameContainer:
		return storagePoolVolumeTypeContainer, nil
	case storagePoolVolumeTypeNameImage:
		return storagePoolVolumeTypeImage, nil
	case storagePoolVolumeTypeNameCustom:
		return storagePoolVolumeTypeCustom, nil
	}

	return -1, fmt.Errorf("Invalid storage volume type name.")
}

func storagePoolVolumeTypeNameToApiEndpoint(volumeTypeName string) (string, error) {
	switch volumeTypeName {
	case storagePoolVolumeTypeNameContainer:
		return storagePoolVolumeApiEndpointContainers, nil
	case storagePoolVolumeTypeNameImage:
		return storagePoolVolumeApiEndpointImages, nil
	case storagePoolVolumeTypeNameCustom:
		return storagePoolVolumeApiEndpointCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type name.")
}

func storagePoolVolumeTypeToName(volumeType int) (string, error) {
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		return storagePoolVolumeTypeNameContainer, nil
	case storagePoolVolumeTypeImage:
		return storagePoolVolumeTypeNameImage, nil
	case storagePoolVolumeTypeCustom:
		return storagePoolVolumeTypeNameCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type.")
}

func storagePoolVolumeTypeToApiEndpoint(volumeType int) (string, error) {
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		return storagePoolVolumeApiEndpointContainers, nil
	case storagePoolVolumeTypeImage:
		return storagePoolVolumeApiEndpointImages, nil
	case storagePoolVolumeTypeCustom:
		return storagePoolVolumeApiEndpointCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type.")
}

func storagePoolVolumeUpdate(d *Daemon, poolName string, volumeName string, volumeType int, newConfig map[string]string) error {
	s, err := storagePoolVolumeInit(d, poolName, volumeName, volumeType)
	if err != nil {
		return err
	}

	oldWritable := s.GetStoragePoolVolumeWritable()
	newWritable := oldWritable

	// Backup the current state
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

	// Skip on no change
	if len(changedConfig) == 0 {
		return nil
	}

	// Update the storage pool
	if !userOnly {
		err = s.StoragePoolVolumeUpdate(changedConfig)
		if err != nil {
			return err
		}
	}

	newWritable.Config = newConfig

	// Apply the new configuration
	s.SetStoragePoolVolumeWritable(&newWritable)

	poolID, err := dbStoragePoolGetID(d.db, poolName)
	if err != nil {
		return err
	}

	// Update the database
	err = dbStoragePoolVolumeUpdate(d.db, volumeName, volumeType, poolID, newConfig)
	if err != nil {
		return err
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}

func storagePoolVolumeUsedByGet(d *Daemon, volumeName string, volumeTypeName string) ([]string, error) {
	// Look for containers using the interface
	cts, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return []string{}, err
	}

	volumeUsedBy := []string{}
	for _, ct := range cts {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			continue
		}

		for _, d := range c.LocalDevices() {
			if d["type"] != "disk" {
				continue
			}

			apiEndpoint, err := storagePoolVolumeTypeNameToApiEndpoint(volumeTypeName)
			if err != nil {
				return []string{}, err
			}

			mustBeEqualTo := ""
			switch apiEndpoint {
			case storagePoolVolumeApiEndpointImages:
				mustBeEqualTo = fmt.Sprintf("%s/%s", apiEndpoint, volumeName)
			case storagePoolVolumeApiEndpointContainers:
				mustBeEqualTo = fmt.Sprintf("%s/%s", apiEndpoint, volumeName)
			default:
				mustBeEqualTo = volumeName
			}
			if d["source"] == mustBeEqualTo {
				volumeUsedBy = append(volumeUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
			}
		}
	}

	return volumeUsedBy, nil
}
