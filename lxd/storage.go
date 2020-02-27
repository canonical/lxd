package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

func init() {
	// Expose storageVolumeMount to the device package as StorageVolumeMount.
	device.StorageVolumeMount = storageVolumeMount

	// Expose storageVolumeUmount to the device package as StorageVolumeUmount.
	device.StorageVolumeUmount = storageVolumeUmount

	// Expose storageRootFSApplyQuota to the device package as StorageRootFSApplyQuota.
	device.StorageRootFSApplyQuota = storageRootFSApplyQuota
}

// Simply cache used to storage the activated drivers on this LXD instance. This
// allows us to avoid querying the database everytime and API call is made.
var storagePoolDriversCacheVal atomic.Value
var storagePoolDriversCacheLock sync.Mutex

func readStoragePoolDriversCache() map[string]string {
	drivers := storagePoolDriversCacheVal.Load()
	if drivers == nil {
		return map[string]string{}
	}

	return drivers.(map[string]string)
}

func storagePoolVolumeAttachPrepare(s *state.State, poolName string, volumeName string, volumeType int, c instance.Container) error {
	// Load the DB records
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	_, volume, err := s.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	poolVolumePut := volume.Writable()

	// Check if unmapped
	if shared.IsTrue(poolVolumePut.Config["security.unmapped"]) {
		// No need to look at containers and maps for unmapped volumes
		return nil
	}

	// Get the on-disk idmap for the volume
	var lastIdmap *idmap.IdmapSet
	if poolVolumePut.Config["volatile.idmap.last"] != "" {
		lastIdmap, err = idmapsetFromString(poolVolumePut.Config["volatile.idmap.last"])
		if err != nil {
			logger.Errorf("Failed to unmarshal last idmapping: %s", poolVolumePut.Config["volatile.idmap.last"])
			return err
		}
	}

	var nextIdmap *idmap.IdmapSet
	nextJsonMap := "[]"
	if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
		// Get the container's idmap
		if c.IsRunning() {
			nextIdmap, err = c.CurrentIdmap()
		} else {
			nextIdmap, err = c.NextIdmap()
		}
		if err != nil {
			return err
		}

		if nextIdmap != nil {
			nextJsonMap, err = idmapsetToJSON(nextIdmap)
			if err != nil {
				return err
			}
		}
	}
	poolVolumePut.Config["volatile.idmap.next"] = nextJsonMap

	// Get mountpoint of storage volume
	remapPath := storagePools.GetStoragePoolVolumeMountPoint(poolName, volumeName)

	if !nextIdmap.Equals(lastIdmap) {
		logger.Debugf("Shifting storage volume")

		if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
			volumeUsedBy, err := storagePoolVolumeUsedByInstancesGet(s, "default", poolName, volumeName)
			if err != nil {
				return err
			}

			if len(volumeUsedBy) > 1 {
				for _, ctName := range volumeUsedBy {
					instt, err := instance.LoadByProjectAndName(s, c.Project(), ctName)
					if err != nil {
						continue
					}

					if instt.Type() != instancetype.Container {
						continue
					}

					ct := instt.(instance.Container)

					var ctNextIdmap *idmap.IdmapSet
					if ct.IsRunning() {
						ctNextIdmap, err = ct.CurrentIdmap()
					} else {
						ctNextIdmap, err = ct.NextIdmap()
					}
					if err != nil {
						return fmt.Errorf("Failed to retrieve idmap of container")
					}

					if !nextIdmap.Equals(ctNextIdmap) {
						return fmt.Errorf("Idmaps of container %v and storage volume %v are not identical", ctName, volumeName)
					}
				}
			} else if len(volumeUsedBy) == 1 {
				// If we're the only one who's attached that container
				// we can shift the storage volume.
				// I'm not sure if we want some locking here.
				if volumeUsedBy[0] != c.Name() {
					return fmt.Errorf("idmaps of container and storage volume are not identical")
				}
			}
		}

		// Unshift rootfs
		if lastIdmap != nil {
			var err error

			if pool.Driver == "zfs" {
				err = lastIdmap.UnshiftRootfs(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = lastIdmap.UnshiftRootfs(remapPath, nil)
			}

			if err != nil {
				logger.Errorf("Failed to unshift \"%s\"", remapPath)
				return err
			}

			logger.Debugf("Unshifted \"%s\"", remapPath)
		}

		// Shift rootfs
		if nextIdmap != nil {
			var err error

			if pool.Driver == "zfs" {
				err = nextIdmap.ShiftRootfs(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = nextIdmap.ShiftRootfs(remapPath, nil)
			}

			if err != nil {
				logger.Errorf("Failed to shift \"%s\"", remapPath)
				return err
			}

			logger.Debugf("Shifted \"%s\"", remapPath)
		}
		logger.Debugf("Shifted storage volume")
	}

	jsonIdmap := "[]"
	if nextIdmap != nil {
		var err error
		jsonIdmap, err = idmapsetToJSON(nextIdmap)
		if err != nil {
			logger.Errorf("Failed to marshal idmap")
			return err
		}
	}

	// Update last idmap
	poolVolumePut.Config["volatile.idmap.last"] = jsonIdmap

	err = s.Cluster.StoragePoolVolumeUpdateByProject("default", volumeName, volumeType, poolID, poolVolumePut.Description, poolVolumePut.Config)
	if err != nil {
		return err
	}

	return nil
}

func resetContainerDiskIdmap(container instance.Container, srcIdmap *idmap.IdmapSet) error {
	dstIdmap, err := container.DiskIdmap()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.IdmapSet)
	}

	if !srcIdmap.Equals(dstIdmap) {
		var jsonIdmap string
		if srcIdmap != nil {
			idmapBytes, err := json.Marshal(srcIdmap.Idmap)
			if err != nil {
				return err
			}
			jsonIdmap = string(idmapBytes)
		} else {
			jsonIdmap = "[]"
		}

		err := container.VolatileSet(map[string]string{"volatile.last_state.idmap": jsonIdmap})
		if err != nil {
			return err
		}
	}

	return nil
}

func setupStorageDriver(s *state.State, forceCheck bool) error {
	pools, err := s.Cluster.StoragePoolsNotPending()
	if err != nil {
		if err == db.ErrNoSuchObject {
			logger.Debugf("No existing storage pools detected")
			return nil
		}
		logger.Debugf("Failed to retrieve existing storage pools")
		return err
	}

	// In case the daemon got killed during upgrade we will already have a
	// valid storage pool entry but it might have gotten messed up and so we
	// cannot perform StoragePoolCheck(). This case can be detected by
	// looking at the patches db: If we already have a storage pool defined
	// but the upgrade somehow got messed up then there will be no
	// "storage_api" entry in the db.
	if len(pools) > 0 && !forceCheck {
		appliedPatches, err := s.Node.Patches()
		if err != nil {
			return err
		}

		if !shared.StringInSlice("storage_api", appliedPatches) {
			logger.Warnf("Incorrectly applied \"storage_api\" patch, skipping storage pool initialization as it might be corrupt")
			return nil
		}

	}

	for _, poolName := range pools {
		logger.Debugf("Initializing and checking storage pool %q", poolName)
		errPrefix := fmt.Sprintf("Failed initializing storage pool %q", poolName)

		pool, err := storagePools.GetPoolByName(s, poolName)
		if err != nil {
			return errors.Wrap(err, errPrefix)
		}

		_, err = pool.Mount()
		if err != nil {
			return errors.Wrap(err, errPrefix)
		}
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s)
	return nil
}

func storagePoolDriversCacheUpdate(s *state.State) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)

	drivers, err := s.Cluster.StoragePoolsGetDrivers()
	if err != nil && err != db.ErrNoSuchObject {
		return
	}

	data := map[string]string{}

	// Get the driver info.
	info := storageDrivers.SupportedDrivers(s)
	for _, entry := range info {
		if shared.StringInSlice(entry.Name, drivers) {
			data[entry.Name] = entry.Version
		}
	}

	// Prepare the cache entries.
	backends := []string{}
	for k, v := range data {
		backends = append(backends, fmt.Sprintf("%s %s", k, v))
	}

	// Update the user agent.
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolDriversCacheVal.Store(data)
	storagePoolDriversCacheLock.Unlock()

	return
}

// storageVolumeMount initialises a new storage interface and checks the pool and volume are
// mounted. If they are not then they are mounted.
func storageVolumeMount(state *state.State, poolName string, volumeName string, volumeTypeName string, inst instance.Instance) error {
	c, ok := inst.(instance.Container)
	if !ok {
		return fmt.Errorf("Received non-LXC container instance")
	}

	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return err
	}

	pool, err := storagePools.GetPoolByName(state, poolName)
	if err != nil {
		return err
	}

	// Mount the storage volume.
	ourMount, err := pool.MountCustomVolume(volumeName, nil)
	if err != nil {
		return err
	}

	revert := true
	if ourMount {
		defer func() {
			if !revert {
				return
			}

			pool.UnmountCustomVolume(volumeName, nil)
		}()
	}

	// Custom storage volumes do not currently support projects, so hardcode "default" project.
	err = storagePoolVolumeAttachPrepare(state, poolName, volumeName, volumeType, c)
	if err != nil {
		return err
	}

	revert = false
	return nil
}

// storageVolumeUmount unmounts a storage volume on a pool.
func storageVolumeUmount(state *state.State, poolName string, volumeName string, volumeType int) error {
	pool, err := storagePools.GetPoolByName(state, poolName)
	if err != nil {
		return err
	}

	_, err = pool.UnmountCustomVolume(volumeName, nil)
	if err != nil {
		return err
	}

	return nil
}

// storageRootFSApplyQuota applies a quota to an instance if it can, if it cannot then it will
// return false indicating that the quota needs to be stored in volatile to be applied on next boot.
func storageRootFSApplyQuota(state *state.State, inst instance.Instance, size string) error {
	pool, err := storagePools.GetPoolByInstance(state, inst)
	if err != nil {
		return err
	}

	err = pool.SetInstanceQuota(inst, size, nil)
	if err != nil {
		return err
	}

	return nil
}
