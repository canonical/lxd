package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// lxdStorageLockMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var lxdStorageOngoingOperationMap = map[string]chan bool{}

// lxdStorageMapLock is used to access lxdStorageOngoingOperationMap.
var lxdStorageMapLock sync.Mutex

// The following functions are used to construct simple operation codes that are
// unique.
func getPoolMountLockID(poolName string) string {
	return fmt.Sprintf("mount/pool/%s", poolName)
}

func getPoolUmountLockID(poolName string) string {
	return fmt.Sprintf("umount/pool/%s", poolName)
}

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
	storageTypeBtrfs storageType = iota
	storageTypeCeph
	storageTypeDir
	storageTypeLvm
	storageTypeMock
	storageTypeZfs
)

var supportedStoragePoolDrivers = []string{"btrfs", "ceph", "dir", "lvm", "zfs"}

func storageTypeToString(sType storageType) (string, error) {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs", nil
	case storageTypeCeph:
		return "ceph", nil
	case storageTypeDir:
		return "dir", nil
	case storageTypeLvm:
		return "lvm", nil
	case storageTypeMock:
		return "mock", nil
	case storageTypeZfs:
		return "zfs", nil
	}

	return "", fmt.Errorf("invalid storage type")
}

func storageStringToType(sName string) (storageType, error) {
	switch sName {
	case "btrfs":
		return storageTypeBtrfs, nil
	case "ceph":
		return storageTypeCeph, nil
	case "dir":
		return storageTypeDir, nil
	case "lvm":
		return storageTypeLvm, nil
	case "mock":
		return storageTypeMock, nil
	case "zfs":
		return storageTypeZfs, nil
	}

	return -1, fmt.Errorf("invalid storage type name")
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
	ContainerCreate(container container) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(c container, fingerprint string) error
	ContainerCanRestore(target container, source container) error
	ContainerDelete(c container) error
	ContainerCopy(target container, source container, containerOnly bool) error
	ContainerRefresh(target container, source container, snapshots []container) error
	ContainerMount(c container) (bool, error)
	ContainerUmount(c container, path string) (bool, error)
	ContainerRename(container container, newName string) error
	ContainerRestore(container container, sourceContainer container) error
	ContainerGetUsage(container container) (int64, error)
	GetContainerPoolInfo() (int64, string, string)
	ContainerStorageReady(container container) bool

	ContainerSnapshotCreate(target container, source container) error
	ContainerSnapshotDelete(c container) error
	ContainerSnapshotRename(c container, newName string) error
	ContainerSnapshotStart(c container) (bool, error)
	ContainerSnapshotStop(c container) (bool, error)

	ContainerBackupCreate(backup backup, sourceContainer container) error
	ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error

	// For use in migrating snapshots.
	ContainerSnapshotCreateEmpty(c container) error

	// Functions dealing with image storage volumes.
	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error
	ImageMount(fingerprint string) (bool, error)
	ImageUmount(fingerprint string) (bool, error)

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
	MigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error

	StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	StorageMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error
}

func storageCoreInit(driver string) (storage, error) {
	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		err = btrfs.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &btrfs, nil
	case storageTypeDir:
		dir := storageDir{}
		err = dir.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &dir, nil
	case storageTypeCeph:
		ceph := storageCeph{}
		err = ceph.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &ceph, nil
	case storageTypeLvm:
		lvm := storageLvm{}
		err = lvm.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &lvm, nil
	case storageTypeMock:
		mock := storageMock{}
		err = mock.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &mock, nil
	case storageTypeZfs:
		zfs := storageZfs{}
		err = zfs.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &zfs, nil
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
	if volumeName != "" && volumeType >= 0 {
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
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		btrfs.poolID = poolID
		btrfs.pool = pool
		btrfs.volume = volume
		btrfs.s = s
		err = btrfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &btrfs, nil
	case storageTypeDir:
		dir := storageDir{}
		dir.poolID = poolID
		dir.pool = pool
		dir.volume = volume
		dir.s = s
		err = dir.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &dir, nil
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
	case storageTypeLvm:
		lvm := storageLvm{}
		lvm.poolID = poolID
		lvm.pool = pool
		lvm.volume = volume
		lvm.s = s
		err = lvm.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &lvm, nil
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
	case storageTypeZfs:
		zfs := storageZfs{}
		zfs.poolID = poolID
		zfs.pool = pool
		zfs.volume = volume
		zfs.s = s
		err = zfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &zfs, nil
	}

	return nil, fmt.Errorf("invalid storage type")
}

func storagePoolInit(s *state.State, poolName string) (storage, error) {
	return storageInit(s, "default", poolName, "", -1)
}

func storagePoolVolumeAttachInit(s *state.State, poolName string, volumeName string, volumeType int, c container) (storage, error) {
	st, err := storageInit(s, "default", poolName, volumeName, volumeType)
	if err != nil {
		return nil, err
	}

	poolVolumePut := st.GetStoragePoolVolumeWritable()

	// Check if unmapped
	if shared.IsTrue(poolVolumePut.Config["security.unmapped"]) {
		// No need to look at containers and maps for unmapped volumes
		return st, nil
	}

	// get last idmapset
	var lastIdmap *idmap.IdmapSet
	if poolVolumePut.Config["volatile.idmap.last"] != "" {
		lastIdmap, err = idmapsetFromString(poolVolumePut.Config["volatile.idmap.last"])
		if err != nil {
			logger.Errorf("Failed to unmarshal last idmapping: %s", poolVolumePut.Config["volatile.idmap.last"])
			return nil, err
		}
	}

	// get next idmapset
	nextIdmap, err := c.IdmapSet()
	if err != nil {
		return nil, err
	}

	nextJsonMap := "[]"
	if nextIdmap != nil {
		nextJsonMap, err = idmapsetToJSON(nextIdmap)
		if err != nil {
			return nil, err
		}
	}
	poolVolumePut.Config["volatile.idmap.next"] = nextJsonMap

	// get mountpoint of storage volume
	remapPath := getStoragePoolVolumeMountPoint(poolName, volumeName)

	// Convert the volume type name to our internal integer representation.
	volumeTypeName, err := storagePoolVolumeTypeToName(volumeType)
	if err != nil {
		return nil, err
	}

	if !reflect.DeepEqual(nextIdmap, lastIdmap) {
		logger.Debugf("Shifting storage volume")
		volumeUsedBy, err := storagePoolVolumeUsedByContainersGet(s,
			"default", volumeName, volumeTypeName)
		if err != nil {
			return nil, err
		}

		if len(volumeUsedBy) > 1 {
			for _, ctName := range volumeUsedBy {
				ct, err := containerLoadByProjectAndName(s, c.Project(), ctName)
				if err != nil {
					continue
				}

				ctNextIdmap, err := ct.IdmapSet()
				if err != nil {
					return nil, fmt.Errorf("Failed to retrieve idmap of container")
				}

				if !reflect.DeepEqual(nextIdmap, ctNextIdmap) {
					return nil, fmt.Errorf("Idmaps of container %v and storage volume %v are not identical", ctNextIdmap, nextIdmap)
				}
			}
		} else if len(volumeUsedBy) == 1 {
			// If we're the only one who's attached that container
			// we can shift the storage volume.
			// I'm not sure if we want some locking here.
			if volumeUsedBy[0] != c.Name() {
				return nil, fmt.Errorf("idmaps of container and storage volume are not identical")
			}
		}

		// mount storage volume
		ourMount, err := st.StoragePoolVolumeMount()
		if err != nil {
			return nil, err
		}
		if ourMount {
			defer func() {
				_, err := st.StoragePoolVolumeUmount()
				if err != nil {
					logger.Warnf("Failed to unmount storage volume")
				}
			}()
		}

		// unshift rootfs
		if lastIdmap != nil {
			var err error

			if st.GetStorageType() == storageTypeZfs {
				err = lastIdmap.UnshiftRootfs(remapPath, zfsIdmapSetSkipper)
			} else {
				err = lastIdmap.UnshiftRootfs(remapPath, nil)
			}
			if err != nil {
				logger.Errorf("Failed to unshift \"%s\"", remapPath)
				return nil, err
			}
			logger.Debugf("Unshifted \"%s\"", remapPath)
		}

		// shift rootfs
		if nextIdmap != nil {
			var err error

			if st.GetStorageType() == storageTypeZfs {
				err = nextIdmap.ShiftRootfs(remapPath, zfsIdmapSetSkipper)
			} else {
				err = nextIdmap.ShiftRootfs(remapPath, nil)
			}
			if err != nil {
				logger.Errorf("Failed to shift \"%s\"", remapPath)
				return nil, err
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
			return nil, err
		}
	}

	// update last idmap
	poolVolumePut.Config["volatile.idmap.last"] = jsonIdmap

	st.SetStoragePoolVolumeWritable(&poolVolumePut)

	poolID, err := s.Cluster.StoragePoolGetID(poolName)
	if err != nil {
		return nil, err
	}
	err = s.Cluster.StoragePoolVolumeUpdate(volumeName, volumeType, poolID, poolVolumePut.Description, poolVolumePut.Config)
	if err != nil {
		return nil, err
	}

	return st, nil
}

func storagePoolVolumeInit(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// No need to detect storage here, its a new container.
	return storageInit(s, project, poolName, volumeName, volumeType)
}

func storagePoolVolumeImageInit(s *state.State, poolName string, imageFingerprint string) (storage, error) {
	return storagePoolVolumeInit(s, "default", poolName, imageFingerprint, storagePoolVolumeTypeImage)
}

func storagePoolVolumeContainerCreateInit(s *state.State, project string, poolName string, containerName string) (storage, error) {
	return storagePoolVolumeInit(s, project, poolName, containerName, storagePoolVolumeTypeContainer)
}

func storagePoolVolumeContainerLoadInit(s *state.State, project, containerName string) (storage, error) {
	// Get the storage pool of a given container.
	poolName, err := s.Cluster.ContainerPool(project, containerName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool for container %q in project %q", containerName, project)
	}

	return storagePoolVolumeInit(s, project, poolName, containerName, storagePoolVolumeTypeContainer)
}

// {LXD_DIR}/storage-pools/<pool>
func getStoragePoolMountPoint(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// ${LXD_DIR}/storage-pools/<pool>/containers/[<project_name>_]<container_name>
func getContainerMountPoint(project string, poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "containers", projectPrefix(project, containerName))
}

// ${LXD_DIR}/storage-pools/<pool>/containers-snapshots/<snapshot_name>
func getSnapshotMountPoint(project, poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "containers-snapshots", projectPrefix(project, snapshotName))
}

// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
func getImageMountPoint(poolName string, fingerprint string) string {
	return shared.VarPath("storage-pools", poolName, "images", fingerprint)
}

// ${LXD_DIR}/storage-pools/<pool>/custom/<storage_volume>
func getStoragePoolVolumeMountPoint(poolName string, volumeName string) string {
	return shared.VarPath("storage-pools", poolName, "custom", volumeName)
}

// ${LXD_DIR}/storage-pools/<pool>/custom-snapshots/<custom volume name>/<snapshot name>
func getStoragePoolVolumeSnapshotMountPoint(poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "custom-snapshots", snapshotName)
}

func createContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
	var mode os.FileMode
	if privileged {
		mode = 0700
	} else {
		mode = 0711
	}

	mntPointSymlinkExist := shared.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := shared.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0711)
		if err != nil {
			return err
		}
	}

	err = os.Chmod(mountPoint, mode)
	if err != nil {
		return err
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(mountPoint, mountPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
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

func createSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	snapshotMntPointExists := shared.PathExists(snapshotMountpoint)
	mntPointSymlinkExist := shared.PathExists(snapshotsSymlink)

	if !snapshotMntPointExists {
		err := os.MkdirAll(snapshotMountpoint, 0711)
		if err != nil {
			return err
		}
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(snapshotsSymlinkTarget, snapshotsSymlink)
		if err != nil {
			return err
		}
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

// ShiftIfNecessary sets the volatile.last_state.idmap key to the idmap last
// used by the container.
func ShiftIfNecessary(container container, srcIdmap *idmap.IdmapSet) error {
	dstIdmap, err := container.IdmapSet()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.IdmapSet)
	}

	if !reflect.DeepEqual(srcIdmap, dstIdmap) {
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

		err := container.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)
		if err != nil {
			return err
		}
	}

	return nil
}

func progressWrapperRender(op *operation, key string, description string, progressInt int64, speedInt int64) {
	meta := op.metadata
	if meta == nil {
		meta = make(map[string]interface{})
	}

	progress := fmt.Sprintf("%s (%s/s)", shared.GetByteSizeString(progressInt, 2), shared.GetByteSizeString(speedInt, 2))
	if description != "" {
		progress = fmt.Sprintf("%s: %s (%s/s)", description, shared.GetByteSizeString(progressInt, 2), shared.GetByteSizeString(speedInt, 2))
	}

	if meta[key] != progress {
		meta[key] = progress
		op.UpdateMetadata(meta)
	}
}

// StorageProgressReader reports the read progress.
func StorageProgressReader(op *operation, key string, description string) func(io.ReadCloser) io.ReadCloser {
	return func(reader io.ReadCloser) io.ReadCloser {
		if op == nil {
			return reader
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		readPipe := &ioprogress.ProgressReader{
			ReadCloser: reader,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return readPipe
	}
}

// StorageProgressWriter reports the write progress.
func StorageProgressWriter(op *operation, key string, description string) func(io.WriteCloser) io.WriteCloser {
	return func(writer io.WriteCloser) io.WriteCloser {
		if op == nil {
			return writer
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		writePipe := &ioprogress.ProgressWriter{
			WriteCloser: writer,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return writePipe
	}
}

func SetupStorageDriver(s *state.State, forceCheck bool) error {
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

	for _, pool := range pools {
		logger.Debugf("Initializing and checking storage pool \"%s\"", pool)
		s, err := storagePoolInit(s, pool)
		if err != nil {
			logger.Errorf("Error initializing storage pool \"%s\": %s, correct functionality of the storage pool cannot be guaranteed", pool, err)
			continue
		}

		err = s.StoragePoolCheck()
		if err != nil {
			return err
		}
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s.Cluster)
	return nil
}

func storagePoolDriversCacheUpdate(cluster *db.Cluster) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)

	drivers, err := cluster.StoragePoolsGetDrivers()
	if err != nil && err != db.ErrNoSuchObject {
		return
	}

	data := map[string]string{}
	for _, driver := range drivers {
		// Initialize a core storage interface for the given driver.
		sCore, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		// Grab the version
		data[driver] = sCore.GetStorageTypeVersion()
	}

	backends := []string{}
	for k, v := range data {
		backends = append(backends, fmt.Sprintf("%s %s", k, v))
	}

	// Update the agent
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolDriversCacheVal.Store(data)
	storagePoolDriversCacheLock.Unlock()

	return
}
