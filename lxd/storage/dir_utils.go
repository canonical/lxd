package storage

import (
	"os"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
)

// DirSnapshotDeleteInternal deletes the given snapshot internally.
func DirSnapshotDeleteInternal(projectName, poolName string, snapshotName string) error {
	snapshotContainerMntPoint := GetSnapshotMountPoint(projectName, poolName, snapshotName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := shared.ContainerGetParentAndSnapshotName(snapshotName)
	snapshotContainerPath := GetSnapshotMountPoint(projectName, poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty {
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
