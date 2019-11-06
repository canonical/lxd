package storage

import (
	"os"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
)

// InstancePath returns the directory of an instance or snapshot.
func InstancePath(instanceType instancetype.Type, projectName, instanceName string, isSnapshot bool) string {
	fullName := project.Prefix(projectName, instanceName)
	if instanceType == instancetype.VM {
		if isSnapshot {
			return shared.VarPath("virtual-machines-snapshots", fullName)
		}

		return shared.VarPath("virtual-machines", fullName)
	}

	if isSnapshot {
		return shared.VarPath("snapshots", fullName)
	}

	return shared.VarPath("containers", fullName)
}

// GetStoragePoolMountPoint returns the mountpoint of the given pool.
// {LXD_DIR}/storage-pools/<pool>
// Deprecated, use GetPoolMountPath in storage/drivers package.
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
	mntPointSymlinkExist := shared.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := shared.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0711)
		if err != nil {
			return err
		}
	}

	err = os.Chmod(mountPoint, 0100)
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
