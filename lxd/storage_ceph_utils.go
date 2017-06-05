package main

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/lxc/lxd/shared"
)

// cephOSDPoolExists checks whether a given OSD pool exists.
func cephOSDPoolExists(ClusterName string, poolName string) bool {
	_, err := shared.RunCommand(
		"ceph",
		"--cluster", ClusterName,
		"osd",
		"pool",
		"get",
		poolName,
		"size")
	if err != nil {
		return false
	}

	return true
}

// cephOSDPoolDestroy destroys an OSD pool.
// - A call to cephOSDPoolDestroy will destroy a pool including any storage
//   volumes that still exist in the pool.
// - In case the OSD pool that is supposed to be deleted does not exist this
//   command will still exit 0. This means that if the caller wants to be sure
//   that this call actually deleted an OSD pool it needs to check for the
//   existence of the pool first.
func cephOSDPoolDestroy(clusterName string, poolName string) error {
	_, err := shared.RunCommand("ceph",
		"--cluster", clusterName,
		"osd",
		"pool",
		"delete",
		poolName,
		poolName,
		"--yes-i-really-really-mean-it")
	if err != nil {
		return err
	}

	return nil
}

// getRBDDevPath constructs the path to a RBD block device.
// Note that for this path to be valid the corresponding volume has to be mapped
// first.
func getRBDDevPath(poolName string, volumeType string, volumeName string) string {
	if volumeType == "" {
		return fmt.Sprintf("/dev/rbd/%s/%s", poolName, volumeName)
	}

	return fmt.Sprintf("/dev/rbd/%s/%s_%s", poolName, volumeType, volumeName)
}

// cephRBDVolumeCreate creates an RBD storage volume.
// Note that the set of features is intentionally limited is intentionally
// limited by passing --image-feature explicitly. This is done to ensure that
// the chances of a conflict between the features supported by the userspace
// library and the kernel module are minimized. Otherwise random panics might
// occur.
func cephRBDVolumeCreate(clusterName string, poolName string, volumeName string,
	volumeType string, size string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--image-feature", "layering,",
		"--cluster", clusterName,
		"--pool", poolName,
		"--size", size,
		"create",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	return err
}

// cephRBDVolumeMap maps a given RBD storage volume
// This will ensure that the RBD storage volume is accessible as a block device
// in the /dev directory and is therefore necessary in order to mount it.
func cephRBDVolumeMap(clusterName string, poolName string, volumeName string,
	volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"map",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EINVAL (already mapped)
					return nil
				}
			}
		}
		return err
	}

	return nil
}

// cephRBDSnapshotCreate creates a read-write snapshot of a given RBD storage
// volume
func cephRBDSnapshotCreate(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"create",
		"--snap", snapshotName,
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return err
	}

	return nil
}

// cephRBDSnapshotProtect protects a given snapshot from being deleted
// This is a precondition to be able to create RBD clones from a given snapshot.
func cephRBDSnapshotProtect(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"protect",
		"--snap", snapshotName,
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 16 {
					// EBUSY (snapshot already protected)
					return nil
				}
			}
		}
		return err
	}

	return nil
}

// cephRBDCloneCreate creates a clone from a protected RBD snapshot
func cephRBDCloneCreate(sourceClusterName string, sourcePoolName string,
	sourceVolumeName string, sourceVolumeType string,
	sourceSnapshotName string, targetPoolName string,
	targetVolumeName string, targetVolumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", sourceClusterName,
		"clone",
		fmt.Sprintf("%s/%s_%s@%s", sourcePoolName, sourceVolumeType,
			sourceVolumeName, sourceSnapshotName),
		fmt.Sprintf("%s/%s_%s", targetPoolName, targetVolumeType,
			targetVolumeName))
	if err != nil {
		return err
	}

	return nil
}
