package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/ioprogress"
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

// lxdStorageLockMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var lxdStorageOngoingOperationMap = map[string]chan bool{}

// lxdStorageMapLock is used to access lxdStorageOngoingOperationMap.
var lxdStorageMapLock sync.Mutex

func getImageCreateLockID(poolName string, fingerprint string) string {
	return fmt.Sprintf("create/image/%s/%s", poolName, fingerprint)
}

func getContainerMountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("mount/container/%s/%s", poolName, containerName)
}

func getContainerUmountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("umount/container/%s/%s", poolName, containerName)
}

func getCustomMountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("mount/custom/%s/%s", poolName, volumeName)
}

func getCustomUmountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("umount/custom/%s/%s", poolName, volumeName)
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

// storageType defines the type of a storage
type storageType int

const (
	storageTypeCeph storageType = iota
	storageTypeMock
)

var supportedStoragePoolDrivers = []string{"btrfs", "ceph", "cephfs", "dir", "lvm", "zfs"}

func storageTypeToString(sType storageType) (string, error) {
	switch sType {
	case storageTypeCeph:
		return "ceph", nil
	case storageTypeMock:
		return "mock", nil
	}

	return "", fmt.Errorf("Invalid storage type")
}

func storageStringToType(sName string) (storageType, error) {
	switch sName {
	case "ceph":
		return storageTypeCeph, nil
	case "mock":
		return storageTypeMock, nil
	}

	return -1, fmt.Errorf("Invalid storage type name")
}

// The storage interface defines the functions needed to implement a storage
// backend for a given storage driver.
type storage interface {
	// Functions dealing with basic driver properties only.
	StorageCoreInit() error
	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string
	GetState() *state.State

	// Functions dealing with storage pools.
	StoragePoolInit() error
	StoragePoolCheck() error
	StoragePoolCreate() error
	StoragePoolDelete() error
	StoragePoolMount() (bool, error)
	StoragePoolUmount() (bool, error)
	StoragePoolResources() (*api.ResourcesStoragePool, error)
	StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error
	GetStoragePoolWritable() api.StoragePoolPut
	SetStoragePoolWritable(writable *api.StoragePoolPut)
	GetStoragePool() *api.StoragePool

	// Functions dealing with custom storage volumes.
	StoragePoolVolumeCreate() error
	StoragePoolVolumeDelete() error
	StoragePoolVolumeMount() (bool, error)
	StoragePoolVolumeUmount() (bool, error)
	StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error
	StoragePoolVolumeRename(newName string) error
	StoragePoolVolumeCopy(source *api.StorageVolumeSource) error
	GetStoragePoolVolumeWritable() api.StorageVolumePut
	SetStoragePoolVolumeWritable(writable *api.StorageVolumePut)
	GetStoragePoolVolume() *api.StorageVolume

	// Functions dealing with custom storage volume snapshots.
	StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error
	StoragePoolVolumeSnapshotDelete() error
	StoragePoolVolumeSnapshotRename(newName string) error

	// Functions dealing with container storage volumes.
	// ContainerCreate creates an empty container (no rootfs/metadata.yaml)
	ContainerCreate(container instance.Instance) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(c instance.Instance, fingerprint string, tracker *ioprogress.ProgressTracker) error
	ContainerDelete(c instance.Instance) error
	ContainerCopy(target instance.Instance, source instance.Instance, containerOnly bool) error
	ContainerRefresh(target instance.Instance, source instance.Instance, snapshots []instance.Instance) error
	ContainerMount(c instance.Instance) (bool, error)
	ContainerUmount(c instance.Instance, path string) (bool, error)
	ContainerRename(container instance.Instance, newName string) error
	ContainerRestore(container instance.Instance, sourceContainer instance.Instance) error
	ContainerGetUsage(container instance.Instance) (int64, error)
	GetContainerPoolInfo() (int64, string, string)
	ContainerStorageReady(container instance.Instance) bool

	ContainerSnapshotCreate(target instance.Instance, source instance.Instance) error
	ContainerSnapshotDelete(c instance.Instance) error
	ContainerSnapshotRename(c instance.Instance, newName string) error
	ContainerSnapshotStart(c instance.Instance) (bool, error)
	ContainerSnapshotStop(c instance.Instance) (bool, error)

	ContainerBackupCreate(path string, backup backup.Backup, sourceContainer instance.Instance) error
	ContainerBackupLoad(info backup.Info, data io.ReadSeeker, tarArgs []string) error

	// For use in migrating snapshots.
	ContainerSnapshotCreateEmpty(c instance.Instance) error

	// Functions dealing with image storage volumes.
	ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error
	ImageDelete(fingerprint string) error

	// Storage type agnostic functions.
	StorageEntitySetQuota(volumeType int, size int64, data interface{}) error

	// Functions dealing with migration.
	MigrationType() migration.MigrationFSType
	// Does this storage backend preserve inodes when it is moved across LXD
	// hosts?
	PreservesInodes() bool

	// Get the pieces required to migrate the source. This contains a list
	// of the "object" (i.e. container or snapshot, depending on whether or
	// not it is a snapshot name) to be migrated in order, and a channel
	// for arguments of the specific migration command. We use a channel
	// here so we don't have to invoke `zfs send` or `rsync` or whatever
	// and keep its stdin/stdout open for each snapshot during the course
	// of migration, we can do it lazily.
	//
	// N.B. that the order here important: e.g. in btrfs/zfs, snapshots
	// which are parents of other snapshots should be sent first, to save
	// as much transfer as possible. However, the base container is always
	// sent as the first object, since that is the grandparent of every
	// snapshot.
	//
	// We leave sending containers which are snapshots of other containers
	// already present on the target instance as an exercise for the
	// enterprising developer.
	MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	MigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error

	StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	StorageMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error
}

func storageCoreInit(driver string) (storage, error) {
	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeCeph:
		ceph := storageCeph{}
		err = ceph.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &ceph, nil
	case storageTypeMock:
		mock := storageMock{}
		err = mock.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &mock, nil
	}

	return nil, fmt.Errorf("invalid storage type")
}

func storageInit(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// Load the storage pool.
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool %q", poolName)
	}

	driver := pool.Driver
	if driver == "" {
		// This shouldn't actually be possible but better safe than
		// sorry.
		return nil, fmt.Errorf("no storage driver was provided")
	}

	// Load the storage volume.
	volume := &api.StorageVolume{}
	if volumeName != "" {
		_, volume, err = s.Cluster.StoragePoolNodeVolumeGetTypeByProject(project, volumeName, volumeType, poolID)
		if err != nil {
			return nil, err
		}
	}

	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeCeph:
		ceph := storageCeph{}
		ceph.poolID = poolID
		ceph.pool = pool
		ceph.volume = volume
		ceph.s = s
		err = ceph.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &ceph, nil
	case storageTypeMock:
		mock := storageMock{}
		mock.poolID = poolID
		mock.pool = pool
		mock.volume = volume
		mock.s = s
		err = mock.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &mock, nil
	}

	return nil, fmt.Errorf("invalid storage type")
}

func storagePoolVolumeAttachPrepare(s *state.State, poolName string, volumeName string, volumeType int, c *containerLXC) error {
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

					ct := instt.(*containerLXC)

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
				err = lastIdmap.UnshiftRootfs(remapPath, shiftZfsSkipper)
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
				err = nextIdmap.ShiftRootfs(remapPath, shiftZfsSkipper)
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

func storagePoolVolumeInit(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// No need to detect storage here, its a new container.
	return storageInit(s, project, poolName, volumeName, volumeType)
}

func storagePoolVolumeContainerLoadInit(s *state.State, project, containerName string) (storage, error) {
	// Get the storage pool of a given container.
	poolName, err := s.Cluster.InstancePool(project, containerName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool for container %q in project %q", containerName, project)
	}

	return storagePoolVolumeInit(s, project, poolName, containerName, storagePoolVolumeTypeContainer)
}

func deleteContainerMountpoint(mountPoint string, mountPointSymlink string, storageTypeName string) error {
	if shared.PathExists(mountPointSymlink) {
		err := os.Remove(mountPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(mountPoint) {
		err := os.Remove(mountPoint)
		if err != nil {
			return err
		}
	}

	if storageTypeName == "" {
		return nil
	}

	mntPointSuffix := storageTypeName
	oldStyleMntPointSymlink := fmt.Sprintf("%s.%s", mountPointSymlink,
		mntPointSuffix)
	if shared.PathExists(oldStyleMntPointSymlink) {
		err := os.Remove(oldStyleMntPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func renameContainerMountpoint(oldMountPoint string, oldMountPointSymlink string, newMountPoint string, newMountPointSymlink string) error {
	if shared.PathExists(oldMountPoint) {
		err := os.Rename(oldMountPoint, newMountPoint)
		if err != nil {
			return err
		}
	}

	// Rename the symlink target.
	if shared.PathExists(oldMountPointSymlink) {
		err := os.Remove(oldMountPointSymlink)
		if err != nil {
			return err
		}
	}

	// Create the new symlink.
	err := os.Symlink(newMountPoint, newMountPointSymlink)
	if err != nil {
		return err
	}

	return nil
}

func deleteSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	if shared.PathExists(snapshotMountpoint) {
		err := os.Remove(snapshotMountpoint)
		if err != nil {
			return err
		}
	}

	couldRemove := false
	if shared.PathExists(snapshotsSymlinkTarget) {
		err := os.Remove(snapshotsSymlinkTarget)
		if err == nil {
			couldRemove = true
		}
	}

	if couldRemove && shared.PathExists(snapshotsSymlink) {
		err := os.Remove(snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func resetContainerDiskIdmap(container *containerLXC, srcIdmap *idmap.IdmapSet) error {
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

	// Handle legacy backends.
	for _, driver := range drivers {
		// Initialize a core storage interface for the given driver.
		sCore, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		// Grab the version.
		data[driver] = sCore.GetStorageTypeVersion()
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
	c, ok := inst.(*containerLXC)
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
