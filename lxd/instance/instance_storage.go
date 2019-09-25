package instance

import (
	"fmt"
	"io"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
)

// Storage interface defines the functions needed to implement a storage
// backend for a given storage driver.
type Storage interface {
	// Functions dealing with basic driver properties only.
	StorageCoreInit() error
	GetStorageType() StorageType
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
	ContainerCreate(container Instance) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(c Instance, fingerprint string, tracker *ioprogress.ProgressTracker) error
	ContainerDelete(c Instance) error
	ContainerCopy(target Instance, source Instance, containerOnly bool) error
	ContainerRefresh(target Instance, source Instance, snapshots []Instance) error
	ContainerMount(c Instance) (bool, error)
	ContainerUmount(c Instance, path string) (bool, error)
	ContainerRename(container Instance, newName string) error
	ContainerRestore(container Instance, sourceContainer Instance) error
	ContainerGetUsage(container Instance) (int64, error)
	GetContainerPoolInfo() (int64, string, string)
	ContainerStorageReady(container Instance) bool

	ContainerSnapshotCreate(target Instance, source Instance) error
	ContainerSnapshotDelete(c Instance) error
	ContainerSnapshotRename(c Instance, newName string) error
	ContainerSnapshotStart(c Instance) (bool, error)
	ContainerSnapshotStop(c Instance) (bool, error)

	ContainerBackupCreate(backup Backup, sourceContainer Instance) error
	ContainerBackupLoad(info BackupInfo, data io.ReadSeeker, tarArgs []string) error

	// For use in migrating snapshots.
	ContainerSnapshotCreateEmpty(c Instance) error

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
	MigrationSink(conn *websocket.Conn, op *operation.Operation, args MigrationSinkArgs) error

	StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	StorageMigrationSink(conn *websocket.Conn, op *operation.Operation, args MigrationSinkArgs) error
}

// storageType defines the type of a storage
type StorageType int

const (
	StorageTypeBtrfs StorageType = iota
	StorageTypeCeph
	StorageTypeCephFs
	StorageTypeDir
	StorageTypeLvm
	StorageTypeMock
	StorageTypeZfs
)

var SupportedStoragePoolDrivers = []string{"btrfs", "ceph", "cephfs", "dir", "lvm", "zfs"}

func StorageTypeToString(sType StorageType) (string, error) {
	switch sType {
	case StorageTypeBtrfs:
		return "btrfs", nil
	case StorageTypeCeph:
		return "ceph", nil
	case StorageTypeCephFs:
		return "cephfs", nil
	case StorageTypeDir:
		return "dir", nil
	case StorageTypeLvm:
		return "lvm", nil
	case StorageTypeMock:
		return "mock", nil
	case StorageTypeZfs:
		return "zfs", nil
	}

	return "", fmt.Errorf("invalid storage type")
}

func StorageStringToType(sName string) (StorageType, error) {
	switch sName {
	case "btrfs":
		return StorageTypeBtrfs, nil
	case "ceph":
		return StorageTypeCeph, nil
	case "cephfs":
		return StorageTypeCephFs, nil
	case "dir":
		return StorageTypeDir, nil
	case "lvm":
		return StorageTypeLvm, nil
	case "mock":
		return StorageTypeMock, nil
	case "zfs":
		return StorageTypeZfs, nil
	}

	return -1, fmt.Errorf("invalid storage type name")
}
