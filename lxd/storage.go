package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
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
var storagePoolDriversCacheInitialized bool
var storagePoolDriversCacheVal atomic.Value
var storagePoolDriversCacheLock sync.Mutex

func readStoragePoolDriversCache() []string {
	drivers := storagePoolDriversCacheVal.Load()
	if drivers == nil {
		return []string{}
	}

	return drivers.([]string)
}

// Filesystem magic numbers
const (
	filesystemSuperMagicTmpfs = 0x01021994
	filesystemSuperMagicExt4  = 0xEF53
	filesystemSuperMagicXfs   = 0x58465342
	filesystemSuperMagicNfs   = 0x6969
	filesystemSuperMagicZfs   = 0x2fc12fc1
)

// filesystemDetect returns the filesystem on which the passed-in path sits.
func filesystemDetect(path string) (string, error) {
	fs := syscall.Statfs_t{}

	err := syscall.Statfs(path, &fs)
	if err != nil {
		return "", err
	}

	switch fs.Type {
	case filesystemSuperMagicBtrfs:
		return "btrfs", nil
	case filesystemSuperMagicZfs:
		return "zfs", nil
	case filesystemSuperMagicTmpfs:
		return "tmpfs", nil
	case filesystemSuperMagicExt4:
		return "ext4", nil
	case filesystemSuperMagicXfs:
		return "xfs", nil
	case filesystemSuperMagicNfs:
		return "nfs", nil
	default:
		shared.LogDebugf("Unknown backing filesystem type: 0x%x", fs.Type)
		return string(fs.Type), nil
	}
}

// storageRsyncCopy copies a directory using rsync (with the --devices option).
func storageRsyncCopy(source string, dest string) (string, error) {
	err := os.MkdirAll(dest, 0755)
	if err != nil {
		return "", err
	}

	rsyncVerbosity := "-q"
	if debug {
		rsyncVerbosity = "-vi"
	}

	output, err := shared.RunCommand(
		"rsync",
		"-a",
		"-HAX",
		"--devices",
		"--delete",
		"--checksum",
		"--numeric-ids",
		rsyncVerbosity,
		shared.AddSlash(source),
		dest)

	return output, err
}

// storageType defines the type of a storage
type storageType int

const (
	storageTypeBtrfs storageType = iota
	storageTypeZfs
	storageTypeLvm
	storageTypeDir
	storageTypeMock
)

var supportedStorageTypes = []string{"btrfs", "zfs", "lvm", "dir"}

func storageTypeToString(sType storageType) (string, error) {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs", nil
	case storageTypeZfs:
		return "zfs", nil
	case storageTypeLvm:
		return "lvm", nil
	case storageTypeMock:
		return "mock", nil
	case storageTypeDir:
		return "dir", nil
	}

	return "", fmt.Errorf("Invalid storage type.")
}

func storageStringToType(sName string) (storageType, error) {
	switch sName {
	case "btrfs":
		return storageTypeBtrfs, nil
	case "zfs":
		return storageTypeZfs, nil
	case "lvm":
		return storageTypeLvm, nil
	case "mock":
		return storageTypeMock, nil
	case "dir":
		return storageTypeDir, nil
	}

	return -1, fmt.Errorf("Invalid storage type name.")
}

// The storage interface defines the functions needed to implement a storage
// backend for a given storage driver.
type storage interface {
	// Functions dealing with basic driver properties only.
	StorageCoreInit() error
	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string

	// Functions dealing with storage pools.
	StoragePoolInit() error
	StoragePoolCheck() error
	StoragePoolCreate() error
	StoragePoolDelete() error
	StoragePoolMount() (bool, error)
	StoragePoolUmount() (bool, error)
	StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error
	GetStoragePoolWritable() api.StoragePoolPut
	SetStoragePoolWritable(writable *api.StoragePoolPut)

	// Functions dealing with custom storage volumes.
	StoragePoolVolumeCreate() error
	StoragePoolVolumeDelete() error
	StoragePoolVolumeMount() (bool, error)
	StoragePoolVolumeUmount() (bool, error)
	StoragePoolVolumeUpdate(changedConfig []string) error
	GetStoragePoolVolumeWritable() api.StorageVolumePut
	SetStoragePoolVolumeWritable(writable *api.StorageVolumePut)

	// Functions dealing with container storage volumes.
	// ContainerCreate creates an empty container (no rootfs/metadata.yaml)
	ContainerCreate(container container) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(container container, imageFingerprint string) error
	ContainerCanRestore(container container, sourceContainer container) error
	ContainerDelete(container container) error
	ContainerCopy(container container, sourceContainer container) error
	ContainerMount(name string, path string) (bool, error)
	ContainerUmount(name string, path string) (bool, error)
	ContainerRename(container container, newName string) error
	ContainerRestore(container container, sourceContainer container) error
	ContainerSetQuota(container container, size int64) error
	ContainerGetUsage(container container) (int64, error)
	GetContainerPoolInfo() (int64, string)
	ContainerStorageReady(name string) bool

	ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error
	ContainerSnapshotDelete(snapshotContainer container) error
	ContainerSnapshotRename(snapshotContainer container, newName string) error
	ContainerSnapshotStart(container container) error
	ContainerSnapshotStop(container container) error

	// For use in migrating snapshots.
	ContainerSnapshotCreateEmpty(snapshotContainer container) error

	// Functions dealing with image storage volumes.
	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error
	ImageMount(fingerprint string) (bool, error)
	ImageUmount(fingerprint string) (bool, error)

	// Functions dealing with migration.
	MigrationType() MigrationFSType
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
	MigrationSource(container container) (MigrationStorageSourceDriver, error)
	MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error
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

	return nil, fmt.Errorf("Invalid storage type.")
}

func storageInit(d *Daemon, poolName string, volumeName string, volumeType int) (storage, error) {
	// Load the storage pool.
	poolID, pool, err := dbStoragePoolGet(d.db, poolName)
	if err != nil {
		return nil, err
	}

	driver := pool.Driver
	if driver == "" {
		// This shouldn't actually be possible but better safe than
		// sorry.
		return nil, fmt.Errorf("No storage driver was provided.")
	}

	// Load the storage volume.
	volume := &api.StorageVolume{}
	if volumeName != "" && volumeType >= 0 {
		_, volume, err = dbStoragePoolVolumeGetType(d.db, volumeName, volumeType, poolID)
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
		btrfs.d = d
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
		dir.d = d
		err = dir.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &dir, nil
	case storageTypeLvm:
		lvm := storageLvm{}
		lvm.poolID = poolID
		lvm.pool = pool
		lvm.volume = volume
		lvm.d = d
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
		mock.d = d
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
		zfs.d = d
		err = zfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &zfs, nil
	}

	return nil, fmt.Errorf("Invalid storage type.")
}

func storagePoolInit(d *Daemon, poolName string) (storage, error) {
	return storageInit(d, poolName, "", -1)
}

func storagePoolVolumeInit(d *Daemon, poolName string, volumeName string, volumeType int) (storage, error) {
	// No need to detect storage here, its a new container.
	return storageInit(d, poolName, volumeName, volumeType)
}

func storagePoolVolumeImageInit(d *Daemon, poolName string, imageFingerprint string) (storage, error) {
	return storagePoolVolumeInit(d, poolName, imageFingerprint, storagePoolVolumeTypeImage)
}

func storagePoolVolumeContainerCreateInit(d *Daemon, poolName string, containerName string) (storage, error) {
	return storagePoolVolumeInit(d, poolName, containerName, storagePoolVolumeTypeContainer)
}

func storagePoolVolumeContainerLoadInit(d *Daemon, containerName string) (storage, error) {
	// Get the storage pool of a given container.
	poolName, err := dbContainerPool(d.db, containerName)
	if err != nil {
		return nil, err
	}

	return storagePoolVolumeInit(d, poolName, containerName, storagePoolVolumeTypeContainer)
}

// {LXD_DIR}/storage-pools/<pool>
func getStoragePoolMountPoint(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// ${LXD_DIR}/storage-pools/<pool>containers/<container_name>
func getContainerMountPoint(poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "containers", containerName)
}

// ${LXD_DIR}/storage-pools/<pool>/snapshots/<snapshot_name>
func getSnapshotMountPoint(poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "snapshots", snapshotName)
}

// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
func getImageMountPoint(poolName string, fingerprint string) string {
	return shared.VarPath("storage-pools", poolName, "images", fingerprint)
}

// ${LXD_DIR}/storage-pools/<pool>/custom/<storage_volume>
func getStoragePoolVolumeMountPoint(poolName string, volumeName string) string {
	return shared.VarPath("storage-pools", poolName, "custom", volumeName)
}

func createContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
	var mode os.FileMode
	if privileged {
		mode = 0700
	} else {
		mode = 0755
	}

	mntPointSymlinkExist := shared.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := shared.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0755)
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
	mntPointSuffix := storageTypeName
	oldStyleMntPointSymlink := fmt.Sprintf("%s.%s", mountPointSymlink, mntPointSuffix)

	if shared.PathExists(mountPointSymlink) {
		err := os.Remove(mountPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(oldStyleMntPointSymlink) {
		err := os.Remove(oldStyleMntPointSymlink)
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

func ShiftIfNecessary(container container, srcIdmap *shared.IdmapSet) error {
	dstIdmap, err := container.IdmapSet()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(shared.IdmapSet)
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

// Useful functions for unreliable backends
func tryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func tryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Unmount(path, flags)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil && err == syscall.EBUSY {
		return err
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
