package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func storagePoolUpdate(state *state.State, name, newDescription string, newConfig map[string]string, withDB bool) error {
	s, err := storagePoolInit(state, name)
	if err != nil {
		return err
	}

	oldWritable := s.GetStoragePoolWritable()
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
			s.SetStoragePoolWritable(&oldWritable)
		}
	}()

	changedConfig, userOnly := storagePools.ConfigDiff(oldConfig, newConfig)
	// Apply config changes if there are any
	if len(changedConfig) != 0 {
		newWritable.Description = newDescription
		newWritable.Config = newConfig

		// Update the storage pool
		if !userOnly {
			if shared.StringInSlice("driver", changedConfig) {
				return fmt.Errorf("the \"driver\" property of a storage pool cannot be changed")
			}

			err = s.StoragePoolUpdate(&newWritable, changedConfig)
			if err != nil {
				return err
			}
		}

		// Apply the new configuration
		s.SetStoragePoolWritable(&newWritable)
	}

	// Update the database if something changed and the withDB flag is true
	// (i.e. this is not a clustering notification.
	if withDB && (len(changedConfig) != 0 || newDescription != oldDescription) {
		err = state.Cluster.StoragePoolUpdate(name, newDescription, newConfig)
		if err != nil {
			return err
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}

// Report all LXD objects that are currently using the given storage pool.
// Volumes of type "custom" are not reported.
// /1.0/containers/alp1
// /1.0/containers/alp1/snapshots/snap0
// /1.0/images/cedce20b5b236f1071134beba7a5fd2aa923fda49eea4c66454dd559a5d6e906
// /1.0/profiles/default
func storagePoolUsedByGet(state *state.State, poolID int64, poolName string) ([]string, error) {
	// Retrieve all non-custom volumes that exist on this storage pool.
	volumes, err := state.Cluster.StoragePoolNodeVolumesGet(poolID, []int{storagePoolVolumeTypeContainer, storagePoolVolumeTypeImage, storagePoolVolumeTypeCustom})
	if err != nil && err != db.ErrNoSuchObject {
		return []string{}, err
	}

	// Retrieve all profiles that exist on this storage pool.
	profiles, err := profilesUsingPoolGetNames(state.Cluster, poolName)

	if err != nil {
		return []string{}, err
	}

	slicelen := len(volumes) + len(profiles)
	if slicelen == 0 {
		return []string{}, nil
	}

	// Save some allocation cycles by preallocating the correct len.
	poolUsedBy := make([]string, slicelen)
	for i := 0; i < len(volumes); i++ {
		apiEndpoint, _ := storagePoolVolumeTypeNameToAPIEndpoint(volumes[i].Type)
		switch apiEndpoint {
		case storagePoolVolumeAPIEndpointContainers:
			if strings.Index(volumes[i].Name, shared.SnapshotDelimiter) > 0 {
				parentName, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(volumes[i].Name)
				poolUsedBy[i] = fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, parentName, snapOnlyName)
			} else {
				poolUsedBy[i] = fmt.Sprintf("/%s/containers/%s", version.APIVersion, volumes[i].Name)
			}
		case storagePoolVolumeAPIEndpointImages:
			poolUsedBy[i] = fmt.Sprintf("/%s/images/%s", version.APIVersion, volumes[i].Name)
		case storagePoolVolumeAPIEndpointCustom:
			poolUsedBy[i] = fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, volumes[i].Type, volumes[i].Name)
		default:
			// If that happens the db is busted, so report an error.
			return []string{}, fmt.Errorf("invalid storage type for storage volume \"%s\"", volumes[i].Name)
		}
	}

	for i := 0; i < len(profiles); i++ {
		poolUsedBy[i+len(volumes)] = fmt.Sprintf("/%s/profiles/%s", version.APIVersion, profiles[i])
	}

	return poolUsedBy, err
}

func profilesUsingPoolGetNames(db *db.Cluster, poolName string) ([]string, error) {
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

		for _, v := range profile.Devices {
			if v["type"] != "disk" {
				continue
			}

			if v["pool"] == poolName {
				usedBy = append(usedBy, pName)
			}
		}
	}

	return usedBy, nil
}

func storagePoolDBCreate(s *state.State, poolName, poolDescription string, driver string, config map[string]string) (int64, error) {
	// Check that the storage pool does not already exist.
	_, err := s.Cluster.StoragePoolGetID(poolName)
	if err == nil {
		return -1, fmt.Errorf("The storage pool already exists")
	}

	// Make sure that we don't pass a nil to the next function.
	if config == nil {
		config = map[string]string{}
	}
	err = storagePoolValidate(poolName, driver, config)
	if err != nil {
		return -1, err
	}

	// Fill in the defaults
	err = storagePoolFillDefault(poolName, driver, config)
	if err != nil {
		return -1, err
	}

	// Create the database entry for the storage pool.
	id, err := dbStoragePoolCreateAndUpdateCache(s.Cluster, poolName, poolDescription, driver, config)
	if err != nil {
		return -1, fmt.Errorf("Error inserting %s into database: %s", poolName, err)
	}

	return id, nil
}

func storagePoolValidate(poolName string, driverName string, config map[string]string) error {
	// Check if the storage pool name is valid.
	err := storagePools.ValidName(poolName)
	if err != nil {
		return err
	}

	// Validate the requested storage pool configuration.
	err = storagePoolValidateConfig(poolName, driverName, config, nil)
	if err != nil {
		return err
	}

	return nil
}

func storagePoolCreateGlobal(state *state.State, req api.StoragePoolsPost) error {
	// Create the database entry.
	id, err := storagePoolDBCreate(state, req.Name, req.Description, req.Driver, req.Config)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path. Track whether or not we want to undo the changes
	// using a closure.
	tryUndo := true
	defer func() {
		if !tryUndo {
			return
		}

		dbStoragePoolDeleteAndUpdateCache(state.Cluster, req.Name)
	}()

	err = storagePoolCreateLocal(state, id, req, false)
	if err != nil {
		return err
	}

	tryUndo = false
	return nil
}

// This performs all non-db related work needed to create the pool.
func storagePoolCreateLocal(state *state.State, id int64, req api.StoragePoolsPost, isNotification bool) error {
	tryUndo := true

	// Make a copy of the req for later diff.
	var updatedConfig map[string]string
	var updatedReq api.StoragePoolsPost
	shared.DeepCopy(&req, &updatedReq)

	// Attempt to create using the new storage pool logic.
	pool, err := storagePools.CreatePool(state, id, &updatedReq, isNotification, nil)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return err
		}

		// Mount the pool
		_, err = pool.Mount()
		if err != nil {
			return err
		}

		// Record the updated config.
		updatedConfig = updatedReq.Config

		// Setup revert function.
		defer func() {
			if !tryUndo {
				return
			}

			pool.Delete(isNotification, nil)
		}()
	} else {
		// Load the old storage struct
		s, err := storagePoolInit(state, req.Name)
		if err != nil {
			return err
		}

		// If this is a clustering notification for a ceph storage, we don't
		// want this node to actually create the pool, as it's already been
		// done by the node that triggered this notification. We just need to
		// create the storage pool directory.
		if s, ok := s.(*storageCeph); ok && isNotification {
			volumeMntPoint := storagePools.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
			return os.MkdirAll(volumeMntPoint, 0711)
		}

		// Create the pool
		err = s.StoragePoolCreate()
		if err != nil {
			return err
		}

		updatedConfig = s.GetStoragePoolWritable().Config

		defer func() {
			if !tryUndo {
				return
			}
			s.StoragePoolDelete()
		}()
	}

	// In case the storage pool config was changed during the pool creation,
	// we need to update the database to reflect this change. This can e.g.
	// happen, when we create a loop file image. This means we append ".img"
	// to the path the user gave us and update the config in the storage
	// callback. So diff the config here to see if something like this has
	// happened.
	configDiff, _ := storagePools.ConfigDiff(req.Config, updatedConfig)
	if len(configDiff) > 0 {
		// Create the database entry for the storage pool.
		err = state.Cluster.StoragePoolUpdate(req.Name, req.Description, updatedConfig)
		if err != nil {
			return fmt.Errorf("Error inserting %s into database: %s", req.Name, err)
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	tryUndo = false

	return nil
}

// Helper around the low-level DB API, which also updates the driver names cache.
func dbStoragePoolCreateAndUpdateCache(db *db.Cluster, poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	id, err := db.StoragePoolCreate(poolName, poolDescription, poolDriver, poolConfig)
	if err != nil {
		return id, err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(db)

	return id, nil
}

// Helper around the low-level DB API, which also updates the driver names
// cache.
func dbStoragePoolDeleteAndUpdateCache(db *db.Cluster, poolName string) error {
	_, err := db.StoragePoolDelete(poolName)
	if err != nil {
		return err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(db)

	return err
}
