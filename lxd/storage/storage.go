package storage

import (
	"os"

	"github.com/grant-he/lxd/lxd/instance/instancetype"
	"github.com/grant-he/lxd/lxd/project"
	"github.com/grant-he/lxd/shared"
)

// InstancePath returns the directory of an instance or snapshot.
func InstancePath(instanceType instancetype.Type, projectName, instanceName string, isSnapshot bool) string {
	fullName := project.Instance(projectName, instanceName)
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

// InstanceImportingFilePath returns the file path used to indicate an instance import is in progress.
// This marker file is created when using `lxd import` to import an instance that exists on the storage device
// but does not exist in the LXD database. The presence of this file causes the instance not to be removed from
// the storage device if the import should fail for some reason.
func InstanceImportingFilePath(instanceType instancetype.Type, poolName, projectName, instanceName string) string {
	fullName := project.Instance(projectName, instanceName)

	typeDir := "containers"
	if instanceType == instancetype.VM {
		typeDir = "virtual-machines"
	}

	return shared.VarPath("storage-pools", poolName, typeDir, fullName, ".importing")
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
	return shared.VarPath("storage-pools", poolName, "containers", project.Instance(projectName, containerName))
}

// GetSnapshotMountPoint returns the mountpoint of the given container snapshot.
// ${LXD_DIR}/storage-pools/<pool>/containers-snapshots/<snapshot_name>
func GetSnapshotMountPoint(projectName, poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "containers-snapshots", project.Instance(projectName, snapshotName))
}

// GetImageMountPoint returns the mountpoint of the given image.
// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
func GetImageMountPoint(poolName string, fingerprint string) string {
	return shared.VarPath("storage-pools", poolName, "images", fingerprint)
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
