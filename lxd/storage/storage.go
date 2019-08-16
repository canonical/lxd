package storage

import (
	"os"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var backends = map[string]driver{
	"dir": &dir{},
}

// VolumeType defines the type of a volume
type VolumeType int

const (
	// VolumeTypeContainer represents the container type.
	VolumeTypeContainer VolumeType = iota
	// VolumeTypeContainerSnapshot represents the container snapshot type.
	VolumeTypeContainerSnapshot
	// VolumeTypeCustom represents a custom volume type.
	VolumeTypeCustom
	// VolumeTypeCustomSnapshot represents a custom volume snapshot type.
	VolumeTypeCustomSnapshot
	// VolumeTypeImage represents an image type.
	VolumeTypeImage
	// VolumeTypeImageSnapshot represents an image snapshot type.
	VolumeTypeImageSnapshot
)

type driver interface {
	Driver

	commonInit(s *state.State, pool *api.StoragePool, poolID int64, volume *api.StorageVolume)
	init() error

	UsesThinpool() bool
	GetBlockFilesystem() string
}

// Driver represents a storage backend.
type Driver interface {
	GetVersion() string

	StoragePoolCheck() error
	StoragePoolCreate() error
	StoragePoolDelete() error
	StoragePoolMount() (bool, error)
	StoragePoolUmount() (bool, error)
	StoragePoolResources() (*api.ResourcesStoragePool, error)
	StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error

	VolumeCreate(project string, volumeName string, volumeType VolumeType) error
	VolumeCopy(project, source string, target string, snapshots []string, volumeType VolumeType) error
	VolumeDelete(project string, volumeName string, recursive bool, volumeType VolumeType) error
	VolumeRename(project string, oldName string, newName string, snapshots []string, volumeType VolumeType) error
	VolumeMount(project string, name string, volumeType VolumeType) (bool, error)
	VolumeUmount(project string, name string, volumeType VolumeType) (bool, error)
	VolumeGetUsage(project, name, path string) (int64, error)
	VolumeSetQuota(project, name string, size int64, userns bool, volumeType VolumeType) error
	VolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error
	VolumeReady(project string, name string) bool
	VolumePrepareRestore(sourceName string, targetName string, targetSnapshots []string, f func() error) error
	VolumeRestore(project string, sourceName string, targetName string, volumeType VolumeType) error
	VolumeSnapshotCreate(project string, source string, target string, volumeType VolumeType) error
	VolumeSnapshotCopy(project, source string, target string, volumeType VolumeType) error
	VolumeSnapshotDelete(project string, volumeName string, recursive bool, volumeType VolumeType) error
	VolumeSnapshotRename(project string, oldName string, newName string, volumeType VolumeType) error
	VolumeBackupCreate(path string, project string, source string, snapshots []string, optimized bool) error
	VolumeBackupLoad(backupDir string, project string, containerName string, snapshots []string, privileged bool, optimized bool) error
}

// Init initializes a storage backend.
func Init(driverName string, s *state.State, pool *api.StoragePool, poolID int64, volume *api.StorageVolume) (Driver, error) {
	storageDriver := backends[driverName]

	if storageDriver == nil {
		return nil, ErrUnsupportedStorageDriver
	}

	storageDriver.commonInit(s, pool, poolID, volume)

	err := storageDriver.init()
	if err != nil {
		return nil, err
	}

	return storageDriver, nil
}

// ContainerPath returns the directory of a container or snapshot.
func ContainerPath(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}

// GetStoragePoolMountPoint returns the mountpoint of the given pool.
// {LXD_DIR}/storage-pools/<pool>
func GetStoragePoolMountPoint(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// GetContainerMountPoint returns the mountpoint of the given container.
// ${LXD_DIR}/storage-pools/<pool>/containers/[<project_name>_]<container_name>
func GetContainerMountPoint(projectName string, poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "containers", project.Prefix(projectName, containerName))
}

// GetSnapshotMountPoint returns the mountpoint of the given container snapshot.
// ${LXD_DIR}/storage-pools/<pool>/containers-snapshots/<snapshot_name>
func GetSnapshotMountPoint(projectName, poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "containers-snapshots", project.Prefix(projectName, snapshotName))
}

// GetImageMountPoint returns the mountpoint of the given image.
// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
func GetImageMountPoint(poolName string, fingerprint string) string {
	return shared.VarPath("storage-pools", poolName, "images", fingerprint)
}

// GetStoragePoolVolumeMountPoint returns the mountpoint of the given pool volume.
// ${LXD_DIR}/storage-pools/<pool>/custom/<storage_volume>
func GetStoragePoolVolumeMountPoint(poolName string, volumeName string) string {
	return shared.VarPath("storage-pools", poolName, "custom", volumeName)
}

// GetStoragePoolVolumeSnapshotMountPoint returns the mountpoint of the given pool volume snapshot.
// ${LXD_DIR}/storage-pools/<pool>/custom-snapshots/<custom volume name>/<snapshot name>
func GetStoragePoolVolumeSnapshotMountPoint(poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "custom-snapshots", snapshotName)
}

// CreateContainerMountpoint creates the provided container mountpoint and symlink.
func CreateContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
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

// CreateSnapshotMountpoint creates the provided container snapshot mountpoint
// and symlink.
func CreateSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
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
