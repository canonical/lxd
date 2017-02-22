package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
)

func storagePoolUpdate(d *Daemon, name string, newConfig map[string]string) error {
	s, err := storagePoolInit(d, name)
	if err != nil {
		return err
	}

	oldWritable := s.GetStoragePoolWritable()
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
			s.SetStoragePoolWritable(&oldWritable)
		}
	}()

	changedConfig, userOnly := storageConfigDiff(oldConfig, newConfig)
	// Skip on no change
	if len(changedConfig) == 0 {
		return nil
	}

	// Update the storage pool
	if !userOnly {
		if shared.StringInSlice("driver", changedConfig) {
			return fmt.Errorf("The \"driver\" property of a storage pool cannot be changed.")
		}

		err = s.StoragePoolUpdate(changedConfig)
		if err != nil {
			return err
		}
	}

	newWritable.Config = newConfig

	// Apply the new configuration
	s.SetStoragePoolWritable(&newWritable)

	// Update the database
	err = dbStoragePoolUpdate(d.db, name, newConfig)
	if err != nil {
		return err
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}

// /1.0/containers/alp1
// /1.0/containers/alp1/snapshots/snap0
// /1.0/images/cedce20b5b236f1071134beba7a5fd2aa923fda49eea4c66454dd559a5d6e906
// /1.0/storage-pools/pool1/volumes/custom/vol1
func storagePoolUsedByGet(db *sql.DB, poolID int64, poolName string) ([]string, error) {
	poolVolumes, err := dbStoragePoolVolumesGet(db, poolID)
	if err != nil {
		return []string{}, err
	}

	poolUsedBy := []string{}
	for i := 0; i < len(poolVolumes); i++ {
		apiEndpoint, _ := storagePoolVolumeTypeNameToApiEndpoint(poolVolumes[i].Type)
		switch apiEndpoint {
		case storagePoolVolumeApiEndpointContainers:
			if strings.Index(poolVolumes[i].Name, shared.SnapshotDelimiter) > 0 {
				fields := strings.SplitN(poolVolumes[i].Name, shared.SnapshotDelimiter, 2)
				poolUsedBy = append(poolUsedBy, fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, fields[0], fields[1]))
			} else {
				poolUsedBy = append(poolUsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, poolVolumes[i].Name))
			}
		case storagePoolVolumeApiEndpointImages:
			poolUsedBy = append(poolUsedBy, fmt.Sprintf("/%s/images/%s", version.APIVersion, poolVolumes[i].Name))
		case storagePoolVolumeApiEndpointCustom:
			// noop
		default:
			shared.LogWarnf("Invalid storage type for storage volume \"%s\".", poolVolumes[i].Name)
		}
	}

	return poolUsedBy, err
}
