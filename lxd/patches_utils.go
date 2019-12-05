package main

import (
	"os"

	"github.com/lxc/lxd/lxd/project"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
)

func dirSnapshotDeleteInternal(projectName, poolName string, snapshotName string) error {
	snapshotContainerMntPoint := driver.GetSnapshotMountPoint(projectName, poolName, snapshotName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := shared.InstanceGetParentAndSnapshotName(snapshotName)
	snapshotContainerPath := driver.GetSnapshotMountPoint(projectName, poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
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
