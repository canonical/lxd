package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
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

// cephRBDVolumeExists checks whether a given RBD storage volume exists.
func cephRBDVolumeExists(clusterName string, poolName string, volumeName string,
	volumeType string) bool {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"image-meta",
		"list",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return false
	}
	return true
}

// cephRBDVolumeDelete deletes an RBD storage volume.
// - In case the RBD storage volume that is supposed to be deleted does not
//   exist this command will still exit 0. This means that if the caller wants
//   to be sure that this call actually deleted an RBD storage volume it needs
//   to check for the existence of the pool first.
func cephRBDVolumeDelete(clusterName string, poolName string, volumeName string,
	volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"rm",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return err
	}

	return nil
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

// cephRBDVolumeUnmap unmaps a given RBD storage volume
// This is a precondition in order to delete an RBD storage volume can.
func cephRBDVolumeUnmap(clusterName string, poolName string, volumeName string,
	volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"unmap",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EINVAL (already unmapped)
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

// cephRBDSnapshotsPurge deletes all snapshot of a given RBD storage volume
// Note that this will only succeed if none of the snapshots are protected.
func cephRBDSnapshotsPurge(clusterName string, poolName string,
	volumeName string, volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"purge",
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

// cephRBDSnapshotUnprotect unprotects a given snapshot
// - This is a precondition to be able to delete an RBD snapshot.
// - This command will only succeed if the snapshot does not have any clones.
func cephRBDSnapshotUnprotect(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"unprotect",
		"--snap", snapshotName,
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EBUSY (snapshot already unprotected)
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

// cephRBDSnapshotListClones list all clones of an RBD snapshot
func cephRBDSnapshotListClones(clusterName string, poolName string,
	volumeName string, volumeType string,
	snapshotName string) ([]string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"children",
		"--image", fmt.Sprintf("%s_%s", volumeType, volumeName),
		"--snap", snapshotName)
	if err != nil {
		return nil, err
	}

	msg = strings.TrimSpace(msg)
	clones := strings.Fields(msg)
	if len(clones) == 0 {
		return nil, NoSuchObjectError
	}

	return clones, nil
}

// cephRBDVolumeMarkDeleted marks an RBD storage volume as being in "zombie"
// state
// An RBD storage volume that is in zombie state is not tracked in LXD's
// database anymore but still needs to be kept around for the sake of any
// dependent storage entities in the storage pool. This usually happens when an
// RBD storage volume has protected snapshots; a scenario most common when
// creating a sparse copy of a container or when LXD updated an image and the
// image still has dependent container clones.
func cephRBDVolumeMarkDeleted(clusterName string, poolName string,
	volumeName string, volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"mv",
		fmt.Sprintf("%s/%s_%s", poolName, volumeType, volumeName),
		fmt.Sprintf("%s/zombie_%s_%s", poolName, volumeType, volumeName))
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeUnmarkDeleted unmarks an RBD storage volume as being in "zombie"
// state
// - An RBD storage volume that is in zombie is not tracked in LXD's database
//   anymore but still needs to be kept around for the sake of any dependent
//   storage entities in the storage pool.
// - This function is mostly used when a user has deleted the storage volume of
//   an image from the storage pool and then triggers a container creation. If
//   LXD detects that the storage volume for the given hash already exists in
//   the pool but is marked as "zombie" it will unmark it as a zombie instead of
//   creating another storage volume for the image.
func cephRBDVolumeUnmarkDeleted(clusterName string, poolName string,
	volumeName string, volumeType string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"mv",
		fmt.Sprintf("%s/zombie_%s_%s", poolName, volumeType, volumeName),
		fmt.Sprintf("%s/%s_%s", poolName, volumeType, volumeName))
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeRename renames a given RBD storage volume
// Note that if the snapshot is mapped - which it usually shouldn't be - this
// usually requires that the snapshot be unmapped under its original name, then
// renamed, and finally will be remapped again. If it is not unmapped under its
// original name and the caller maps it under its new name the snapshot will be
// mapped twice. This will prevent it from being deleted.
func cephRBDVolumeSnapshotRename(clusterName string, poolName string,
	volumeName string, volumeType string, oldSnapshotName string,
	newSnapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"snap",
		"rename",
		fmt.Sprintf("%s/%s_%s@%s", poolName, volumeType, volumeName,
			oldSnapshotName),
		fmt.Sprintf("%s/%s_%s@%s", poolName, volumeType, volumeName,
			newSnapshotName))
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeGetParent will return the snapshot the RBD clone was created
// from
// - If the RBD storage volume is not a clone then this function will return
//   NoSuchObjectError.
// - The snapshot will be returned as
//   <osd-pool-name>/<rbd-volume-name>@<rbd-snapshot-name>
//   The caller will usually want to parse this according to its needs. This
//   helper library provides two small functions to do this but see below.
func cephRBDVolumeGetParent(clusterName string, poolName string,
	volumeName string, volumeType string) (string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"info",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return "", err
	}

	idx := strings.Index(msg, "parent: ")
	if idx == -1 {
		return "", NoSuchObjectError
	}

	msg = msg[(idx + len("parent: ")):]
	msg = strings.TrimSpace(msg)

	idx = strings.Index(msg, "\n")
	if idx == -1 {
		return "", fmt.Errorf("Unexpected parsing error")
	}

	msg = msg[:idx]
	msg = strings.TrimSpace(msg)

	return msg, nil
}

// cephRBDSnapshotDelete deletes an RBD snapshot
// This requires that the snapshot does not have any clones and is unmapped and
// unprotected.
func cephRBDSnapshotDelete(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"rm",
		fmt.Sprintf("%s_%s@%s", volumeType, volumeName, snapshotName))
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeCopy copies an RBD storage volume
// This is a non-sparse copy which doesn't introduce any dependency relationship
// between the source RBD storage volume and the target RBD storage volume. The
// operations is similar to creating an empty RBD storage volume and rsyncing
// the contents of the source RBD storage volume into it.
func cephRBDVolumeCopy(clusterName string, oldVolumeName string,
	newVolumeName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--cluster", clusterName,
		"cp",
		oldVolumeName,
		newVolumeName)
	if err != nil {
		return err
	}

	return nil
}

// getRBDSize returns the size the RBD storage volume is supposed to be created
// with
func (s *storageCeph) getRBDSize() (string, error) {
	sz, err := shared.ParseByteSizeString(s.volume.Config["size"])
	if err != nil {
		return "", err
	}

	// Safety net: Set to default value.
	if sz == 0 {
		sz, _ = shared.ParseByteSizeString("10GB")
	}

	return fmt.Sprintf("%dB", sz), nil
}

// getRBDFilesystem returns the filesystem the RBD storage volume is supposed to
// be created with
func (s *storageCeph) getRBDFilesystem() string {
	if s.volume.Config["block.filesystem"] != "" {
		return s.volume.Config["block.filesystem"]
	}

	if s.pool.Config["volume.block.filesystem"] != "" {
		return s.pool.Config["volume.block.filesystem"]
	}

	return "ext4"
}

// getRBDMountOptions returns the mount options the storage volume is supposed
// to be mounted with
// The option string that is returned needs to be passed to the approriate
// helper (currently named "lxdResolveMountoptions") which will take on the job
// of splitting it into appropriate flags and string options.
func (s *storageCeph) getRBDMountOptions() string {
	if s.volume.Config["block.mount_options"] != "" {
		return s.volume.Config["block.mount_options"]
	}

	if s.pool.Config["volume.block.mount_options"] != "" {
		return s.pool.Config["volume.block.mount_options"]
	}

	return "discard"
}

// copyWithoutSnapshotsFull creates a non-sparse copy of a container
// This does not introduce a dependency relation between the source RBD storage
// volume and the target RBD storage volume.
func (s *storageCeph) copyWithoutSnapshotsFull(target container,
	source container) error {
	logger.Debugf("Creating full RBD copy \"%s\" -> \"%s\"", source.Name(),
		target.Name())

	sourceIsSnapshot := source.IsSnapshot()
	sourceContainerName := source.Name()
	targetContainerName := target.Name()
	oldVolumeName := fmt.Sprintf("%s/container_%s", s.OSDPoolName,
		sourceContainerName)
	newVolumeName := fmt.Sprintf("%s/container_%s", s.OSDPoolName,
		targetContainerName)
	if sourceIsSnapshot {
		sourceContainerOnlyName, sourceSnapshotOnlyName, _ :=
			containerGetParentAndSnapshotName(sourceContainerName)
		oldVolumeName = fmt.Sprintf("%s/container_%s@snapshot_%s",
			s.OSDPoolName, sourceContainerOnlyName,
			sourceSnapshotOnlyName)
	}

	err := cephRBDVolumeCopy(s.ClusterName, oldVolumeName, newVolumeName)
	if err != nil {
		logger.Debugf(`Failed to create full RBD copy "%s" -> `+
			`"%s": %s`, source.Name(), target.Name(), err)
		return err
	}

	err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, targetContainerName,
		storagePoolVolumeTypeNameContainer)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for image `+
			`"%s" on storage pool "%s": %s`, targetContainerName,
			s.pool.Name, err)
		return err
	}

	targetContainerMountPoint := getContainerMountPoint(s.pool.Name,
		target.Name())
	err = createContainerMountpoint(targetContainerMountPoint, target.Path(),
		target.IsPrivileged())
	if err != nil {
		return err
	}

	logger.Debugf("Created full RBD copy \"%s\" -> \"%s\"", source.Name(),
		target.Name())
	return nil
}
