package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	"github.com/pborman/uuid"
)

// cephOSDPoolExists checks whether a given OSD pool exists.
func cephOSDPoolExists(ClusterName string, poolName string, userName string) bool {
	_, err := shared.RunCommand(
		"ceph",
		"--name", fmt.Sprintf("client.%s", userName),
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
func cephOSDPoolDestroy(clusterName string, poolName string, userName string) error {
	_, err := shared.RunCommand("ceph",
		"--name", fmt.Sprintf("client.%s", userName),
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

// cephRBDVolumeCreate creates an RBD storage volume.
// Note that the set of features is intentionally limited is intentionally
// limited by passing --image-feature explicitly. This is done to ensure that
// the chances of a conflict between the features supported by the userspace
// library and the kernel module are minimized. Otherwise random panics might
// occur.
func cephRBDVolumeCreate(clusterName string, poolName string, volumeName string,
	volumeType string, size string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	volumeType string, userName string) bool {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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

// cephRBDVolumeSnapshotExists checks whether a given RBD snapshot exists.
func cephRBDSnapshotExists(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string,
	userName string) bool {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"info",
		fmt.Sprintf("%s_%s@%s", volumeType, volumeName, snapshotName))
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
	volumeType string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	volumeType string, userName string) (string, error) {
	devPath, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"map",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return "", err
	}

	idx := strings.Index(devPath, "/dev/rbd")
	if idx < 0 {
		return "", fmt.Errorf("Failed to detect mapped device path")
	}

	devPath = devPath[idx:]
	return strings.TrimSpace(devPath), nil
}

// cephRBDVolumeUnmap unmaps a given RBD storage volume
// This is a precondition in order to delete an RBD storage volume can.
func cephRBDVolumeUnmap(clusterName string, poolName string, volumeName string,
	volumeType string, userName string, unmapUntilEINVAL bool) error {
	unmapImageName := fmt.Sprintf("%s_%s", volumeType, volumeName)

again:
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"unmap",
		unmapImageName)
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

	if unmapUntilEINVAL {
		goto again
	}

	return nil
}

// cephRBDVolumeSnapshotUnmap unmaps a given RBD snapshot
// This is a precondition in order to delete an RBD snapshot can.
func cephRBDVolumeSnapshotUnmap(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string,
	userName string, unmapUntilEINVAL bool) error {
	unmapSnapshotName := fmt.Sprintf("%s_%s@%s", volumeType, volumeName,
		snapshotName)

again:
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"unmap",
		unmapSnapshotName)
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

	if unmapUntilEINVAL {
		goto again
	}

	return nil
}

// cephRBDSnapshotCreate creates a read-write snapshot of a given RBD storage
// volume
func cephRBDSnapshotCreate(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string,
	userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	volumeName string, volumeType string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	volumeName string, volumeType string, snapshotName string,
	userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	volumeName string, volumeType string, snapshotName string,
	userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	targetVolumeName string, targetVolumeType string,
	userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", sourceClusterName,
		"--image-feature", "layering",
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
	snapshotName string, userName string) ([]string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
		return nil, db.ErrNoSuchObject
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
	volumeType string, oldVolumeName string, newVolumeName string,
	userName string, suffix string) error {
	deletedName := fmt.Sprintf("%s/zombie_%s_%s", poolName, volumeType,
		newVolumeName)
	if suffix != "" {
		deletedName = fmt.Sprintf("%s_%s", deletedName, suffix)
	}
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"mv",
		fmt.Sprintf("%s/%s_%s", poolName, volumeType, oldVolumeName),
		deletedName)
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
	volumeName string, volumeType string, userName string, oldSuffix string,
	newSuffix string) error {
	oldName := fmt.Sprintf("%s/zombie_%s_%s", poolName, volumeType, volumeName)
	if oldSuffix != "" {
		oldName = fmt.Sprintf("%s_%s", oldName, oldSuffix)
	}

	newName := fmt.Sprintf("%s/%s_%s", poolName, volumeType, volumeName)
	if newSuffix != "" {
		newName = fmt.Sprintf("%s_%s", newName, newSuffix)
	}

	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"mv",
		oldName,
		newName)
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeRename renames a given RBD storage volume
// Note that this usually requires that the image be unmapped under its original
// name, then renamed, and finally will be remapped again. If it is not unmapped
// under its original name and the callers maps it under its new name the image
// will be mapped twice. This will prevent it from being deleted.
func cephRBDVolumeRename(clusterName string, poolName string, volumeType string,
	oldVolumeName string, newVolumeName string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"mv",
		fmt.Sprintf("%s/%s_%s", poolName, volumeType, oldVolumeName),
		fmt.Sprintf("%s/%s_%s", poolName, volumeType, newVolumeName))
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
	newSnapshotName string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
//   db.NoSuchObjectError.
// - The snapshot will be returned as
//   <osd-pool-name>/<rbd-volume-name>@<rbd-snapshot-name>
//   The caller will usually want to parse this according to its needs. This
//   helper library provides two small functions to do this but see below.
func cephRBDVolumeGetParent(clusterName string, poolName string,
	volumeName string, volumeType string, userName string) (string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"info",
		fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return "", err
	}

	idx := strings.Index(msg, "parent: ")
	if idx == -1 {
		return "", db.ErrNoSuchObject
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
	volumeName string, volumeType string, snapshotName string,
	userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
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
	newVolumeName string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"cp",
		oldVolumeName,
		newVolumeName)
	if err != nil {
		return err
	}

	return nil
}

// cephRBDVolumeListSnapshots retrieves the snapshots of an RBD storage volume
// The format of the snapshot names is simply the part after the @. So given a
// valid RBD path relative to a pool
// <osd-pool-name>/<rbd-storage-volume>@<rbd-snapshot-name>
// this will only return
// <rbd-snapshot-name>
func cephRBDVolumeListSnapshots(clusterName string, poolName string,
	volumeName string, volumeType string,
	userName string) ([]string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--format", "json",
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"ls", fmt.Sprintf("%s_%s", volumeType, volumeName))
	if err != nil {
		return []string{}, err
	}

	var data []map[string]interface{}
	err = json.Unmarshal([]byte(msg), &data)
	if err != nil {
		return []string{}, err
	}

	snapshots := []string{}
	for _, v := range data {
		_, ok := v["name"]
		if !ok {
			return []string{}, fmt.Errorf("No \"name\" property found")
		}

		name, ok := v["name"].(string)
		if !ok {
			return []string{}, fmt.Errorf("\"name\" property did not have string type")
		}

		name = strings.TrimSpace(name)
		snapshots = append(snapshots, name)
	}

	if len(snapshots) == 0 {
		return []string{}, db.ErrNoSuchObject
	}

	return snapshots, nil
}

// cephRBDVolumeRestore restores an RBD storage volume to the state of one of
// its snapshots
func cephRBDVolumeRestore(clusterName string, poolName string, volumeName string,
	volumeType string, snapshotName string, userName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"snap",
		"rollback",
		"--snap", snapshotName,
		fmt.Sprintf("%s_%s", volumeType, volumeName))
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

	if s.getRBDFilesystem() == "btrfs" {
		return "user_subvol_rm_allowed,discard"
	}

	return "discard"
}

// copyWithoutSnapshotsFull creates a non-sparse copy of a container
// This does not introduce a dependency relation between the source RBD storage
// volume and the target RBD storage volume.
func (s *storageCeph) copyWithoutSnapshotsFull(target container,
	source container) error {
	logger.Debugf(`Creating non-sparse copy of RBD storage volume for container "%s" to "%s" without snapshots`, source.Name(), target.Name())

	sourceIsSnapshot := source.IsSnapshot()
	sourceContainerName := projectPrefix(source.Project(), source.Name())
	targetContainerName := projectPrefix(target.Project(), target.Name())
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

	err := cephRBDVolumeCopy(s.ClusterName, oldVolumeName, newVolumeName,
		s.UserName)
	if err != nil {
		logger.Debugf(`Failed to create full RBD copy "%s" to "%s": %s`, source.Name(), target.Name(), err)
		return err
	}

	_, err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, targetContainerName,
		storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for image "%s" on storage pool "%s": %s`, targetContainerName, s.pool.Name, err)
		return err
	}

	targetContainerMountPoint := getContainerMountPoint(target.Project(), s.pool.Name, target.Name())
	err = createContainerMountpoint(targetContainerMountPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	ourMount, err := target.StorageStart()
	if err != nil {
		return err
	}
	if ourMount {
		defer target.StorageStop()
	}

	err = target.TemplateApply("copy")
	if err != nil {
		logger.Errorf(`Failed to apply copy template for container "%s": %s`, target.Name(), err)
		return err
	}
	logger.Debugf(`Applied copy template for container "%s"`, target.Name())

	logger.Debugf(`Created non-sparse copy of RBD storage volume for container "%s" to "%s" without snapshots`, source.Name(),
		target.Name())
	return nil
}

// copyWithoutSnapshotsFull creates a sparse copy of a container
// This introduces a dependency relation between the source RBD storage volume
// and the target RBD storage volume.
func (s *storageCeph) copyWithoutSnapshotsSparse(target container,
	source container) error {
	logger.Debugf(`Creating sparse copy of RBD storage volume for container "%s" to "%s" without snapshots`, source.Name(),
		target.Name())

	sourceIsSnapshot := source.IsSnapshot()
	sourceContainerName := projectPrefix(source.Project(), source.Name())
	targetContainerName := projectPrefix(target.Project(), target.Name())
	sourceContainerOnlyName := sourceContainerName
	sourceSnapshotOnlyName := ""
	snapshotName := fmt.Sprintf("zombie_snapshot_%s",
		uuid.NewRandom().String())
	if sourceIsSnapshot {
		sourceContainerOnlyName, sourceSnapshotOnlyName, _ =
			containerGetParentAndSnapshotName(sourceContainerName)
		snapshotName = fmt.Sprintf("snapshot_%s", sourceSnapshotOnlyName)
	} else {
		// create snapshot
		err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName,
			sourceContainerName, storagePoolVolumeTypeNameContainer,
			snapshotName, s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, targetContainerName, s.pool.Name, err)
			return err
		}
	}

	// protect volume so we can create clones of it
	err := cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName,
		sourceContainerOnlyName, storagePoolVolumeTypeNameContainer,
		snapshotName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to protect snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, snapshotName, s.pool.Name, err)
		return err
	}

	err = cephRBDCloneCreate(s.ClusterName, s.OSDPoolName,
		sourceContainerOnlyName, storagePoolVolumeTypeNameContainer,
		snapshotName, s.OSDPoolName, targetContainerName,
		storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to clone new RBD storage volume for container "%s": %s`, targetContainerName, err)
		return err
	}

	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
		targetContainerName, storagePoolVolumeTypeNameContainer,
		s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for image "%s" on storage pool "%s": %s`, targetContainerName,
			s.pool.Name, err)
		return err
	}

	// Generate a new xfs's UUID
	RBDFilesystem := s.getRBDFilesystem()
	msg, err := fsGenerateNewUUID(RBDFilesystem, RBDDevPath)
	if err != nil {
		logger.Errorf("Failed to create new \"%s\" UUID for container \"%s\" on storage pool \"%s\": %s", RBDFilesystem, targetContainerName, s.pool.Name, msg)
		return err
	}

	targetContainerMountPoint := getContainerMountPoint(target.Project(), s.pool.Name, target.Name())
	err = createContainerMountpoint(targetContainerMountPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	ourMount, err := target.StorageStart()
	if err != nil {
		return err
	}
	if ourMount {
		defer target.StorageStop()
	}

	err = target.TemplateApply("copy")
	if err != nil {
		logger.Errorf(`Failed to apply copy template for container "%s": %s`, target.Name(), err)
		return err
	}
	logger.Debugf(`Applied copy template for container "%s"`, target.Name())

	logger.Debugf(`Created sparse copy of RBD storage volume for container "%s" to "%s" without snapshots`, source.Name(),
		target.Name())
	return nil
}

// copyWithSnapshots creates a non-sparse copy of a container including its
// snapshots
// This does not introduce a dependency relation between the source RBD storage
// volume and the target RBD storage volume.
func (s *storageCeph) copyWithSnapshots(sourceVolumeName string,
	targetVolumeName string, sourceParentSnapshot string) error {
	logger.Debugf(`Creating non-sparse copy of RBD storage volume "%s to "%s"`, sourceVolumeName, targetVolumeName)

	args := []string{
		"export-diff",
		"--id", s.UserName,
		"--cluster", s.ClusterName,
		sourceVolumeName,
	}

	if sourceParentSnapshot != "" {
		args = append(args, "--from-snap", sourceParentSnapshot)
	}

	// redirect output to stdout
	args = append(args, "-")

	rbdSendCmd := exec.Command("rbd", args...)
	rbdRecvCmd := exec.Command(
		"rbd",
		"--id", s.UserName,
		"import-diff",
		"--cluster", s.ClusterName,
		"-",
		targetVolumeName)

	rbdRecvCmd.Stdin, _ = rbdSendCmd.StdoutPipe()
	rbdRecvCmd.Stdout = os.Stdout
	rbdRecvCmd.Stderr = os.Stderr

	err := rbdRecvCmd.Start()
	if err != nil {
		return err
	}

	err = rbdSendCmd.Run()
	if err != nil {
		return err
	}

	err = rbdRecvCmd.Wait()
	if err != nil {
		return err
	}

	logger.Debugf(`Created non-sparse copy of RBD storage volume "%s" to "%s"`, sourceVolumeName, targetVolumeName)
	return nil
}

// cephContainerDelete deletes the RBD storage volume of a container including
// any dependencies
// - This function takes care to delete any RBD storage entities that are marked
//   as zombie and whose existence is solely dependent on the RBD storage volume
//   for the container to be deleted.
// - This function will mark any storage entities of the container to be deleted
//   as zombies in case any RBD storage entities in the storage pool have a
//   dependency relation with it.
// - This function uses a C-style convention to return error or success simply
//   because it is more elegant and simple than the go way.
//   The function will return
//   -1 on error
//    0 if the RBD storage volume has been deleted
//    1 if the RBD storage volume has been marked as a zombie
// - cephContainerDelete in conjunction with cephContainerSnapshotDelete
//   recurses through an OSD storage pool to find and delete any storage
//   entities that were kept around because of dependency relations but are not
//   deletable.
func cephContainerDelete(clusterName string, poolName string, volumeName string,
	volumeType string, userName string) int {
	logEntry := fmt.Sprintf("%s/%s_%s", poolName, volumeType, volumeName)

	snaps, err := cephRBDVolumeListSnapshots(clusterName, poolName,
		volumeName, volumeType, userName)
	if err == nil {
		var zombies int
		for _, snap := range snaps {
			logEntry := fmt.Sprintf("%s/%s_%s@%s", poolName,
				volumeType, volumeName, snap)

			ret := cephContainerSnapshotDelete(clusterName,
				poolName, volumeName, volumeType, snap, userName)
			if ret < 0 {
				logger.Errorf(`Failed to delete RBD storage volume "%s"`, logEntry)
				return -1
			} else if ret == 1 {
				logger.Debugf(`Marked RBD storage volume "%s" as zombie`, logEntry)
				zombies++
			} else {
				logger.Debugf(`Deleted RBD storage volume "%s"`, logEntry)
			}
		}

		if zombies > 0 {
			// unmap
			err = cephRBDVolumeUnmap(clusterName, poolName,
				volumeName, volumeType, userName, true)
			if err != nil {
				logger.Errorf(`Failed to unmap RBD storage volume "%s": %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Unmapped RBD storage volume "%s"`, logEntry)

			if strings.HasPrefix(volumeType, "zombie_") {
				logger.Debugf(`RBD storage volume "%s" already marked as zombie`, logEntry)
				return 1
			}

			newVolumeName := fmt.Sprintf("%s_%s", volumeName,
				uuid.NewRandom().String())
			err := cephRBDVolumeMarkDeleted(clusterName, poolName,
				volumeType, volumeName, newVolumeName, userName,
				"")
			if err != nil {
				logger.Errorf(`Failed to mark RBD storage volume "%s" as zombie: %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Marked RBD storage volume "%s" as zombie`, logEntry)

			return 1
		}
	} else {
		if err != db.ErrNoSuchObject {
			logger.Errorf(`Failed to retrieve snapshots of RBD storage volume: %s`, err)
			return -1
		}

		parent, err := cephRBDVolumeGetParent(clusterName, poolName,
			volumeName, volumeType, userName)
		if err == nil {
			logger.Debugf(`Detected "%s" as parent of RBD storage volume "%s"`, parent, logEntry)
			_, parentVolumeType, parentVolumeName,
				parentSnapshotName, err := parseParent(parent)
			if err != nil {
				logger.Errorf(`Failed to parse parent "%s" of RBD storage volume "%s"`, parent, logEntry)
				return -1
			}
			logger.Debugf(`Split parent "%s" of RBD storage volume "%s" into volume type "%s", volume name "%s", and snapshot name "%s"`, parent, logEntry, parentVolumeType,
				parentVolumeName, parentSnapshotName)

			// unmap
			err = cephRBDVolumeUnmap(clusterName, poolName,
				volumeName, volumeType, userName, true)
			if err != nil {
				logger.Errorf(`Failed to unmap RBD storage volume "%s": %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Unmapped RBD storage volume "%s"`, logEntry)

			// delete
			err = cephRBDVolumeDelete(clusterName, poolName,
				volumeName, volumeType, userName)
			if err != nil {
				logger.Errorf(`Failed to delete RBD storage volume "%s": %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Deleted RBD storage volume "%s"`, logEntry)

			// Only delete the parent snapshot of the container if
			// it is a zombie. If it is not we know that LXD is
			// still using it.
			if strings.HasPrefix(parentVolumeType, "zombie_") ||
				strings.HasPrefix(parentSnapshotName, "zombie_") {
				ret := cephContainerSnapshotDelete(clusterName,
					poolName, parentVolumeName,
					parentVolumeType, parentSnapshotName,
					userName)
				if ret < 0 {
					logger.Errorf(`Failed to delete snapshot "%s" of RBD storage volume "%s"`, parentSnapshotName, logEntry)
					return -1
				}
				logger.Debugf(`Deleteed snapshot "%s" of RBD storage volume "%s"`, parentSnapshotName, logEntry)
			}

			return 0
		} else {
			if err != db.ErrNoSuchObject {
				logger.Errorf(`Failed to retrieve parent of RBD storage volume "%s"`, logEntry)
				return -1
			}
			logger.Debugf(`RBD storage volume "%s" does not have parent`, logEntry)

			// unmap
			err = cephRBDVolumeUnmap(clusterName, poolName,
				volumeName, volumeType, userName, true)
			if err != nil {
				logger.Errorf(`Failed to unmap RBD storage volume "%s": %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Unmapped RBD storage volume "%s"`, logEntry)

			// delete
			err = cephRBDVolumeDelete(clusterName, poolName,
				volumeName, volumeType, userName)
			if err != nil {
				logger.Errorf(`Failed to delete RBD storage volume "%s": %s`, logEntry, err)
				return -1
			}
			logger.Debugf(`Deleted RBD storage volume "%s"`, logEntry)

		}
	}

	return 0
}

// cephContainerSnapshotDelete deletes an RBD snapshot of a container including
// any dependencies
// - This function takes care to delete any RBD storage entities that are marked
//   as zombie and whose existence is solely dependent on the RBD snapshot for
//   the container to be deleted.
// - This function will mark any storage entities of the container to be deleted
//   as zombies in case any RBD storage entities in the storage pool have a
//   dependency relation with it.
// - This function uses a C-style convention to return error or success simply
//   because it is more elegant and simple than the go way.
//   The function will return
//   -1 on error
//    0 if the RBD storage volume has been deleted
//    1 if the RBD storage volume has been marked as a zombie
// - cephContainerSnapshotDelete in conjunction with cephContainerDelete
//   recurses through an OSD storage pool to find and delete any storage
//   entities that were kept around because of dependency relations but are not
//   deletable.
func cephContainerSnapshotDelete(clusterName string, poolName string,
	volumeName string, volumeType string, snapshotName string,
	userName string) int {
	logImageEntry := fmt.Sprintf("%s/%s_%s", poolName, volumeType, volumeName)
	logSnapshotEntry := fmt.Sprintf("%s/%s_%s@%s", poolName, volumeType,
		volumeName, snapshotName)

	clones, err := cephRBDSnapshotListClones(clusterName, poolName,
		volumeName, volumeType, snapshotName, userName)
	if err != nil {
		if err != db.ErrNoSuchObject {
			logger.Errorf(`Failed to list clones of RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
			return -1
		}
		logger.Debugf(`RBD snapshot "%s" of RBD storage volume "%s" does not have any clones`, logSnapshotEntry, logImageEntry)

		// unprotect
		err = cephRBDSnapshotUnprotect(clusterName, poolName, volumeName,
			volumeType, snapshotName, userName)
		if err != nil {
			logger.Errorf(`Failed to unprotect RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
			return -1
		}
		logger.Debugf(`Unprotected RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

		// unmap
		err = cephRBDVolumeSnapshotUnmap(clusterName, poolName,
			volumeName, volumeType, snapshotName, userName, true)
		if err != nil {
			logger.Errorf(`Failed to unmap RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
			return -1
		}
		logger.Debugf(`Unmapped RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

		// delete
		err = cephRBDSnapshotDelete(clusterName, poolName, volumeName,
			volumeType, snapshotName, userName)
		if err != nil {
			logger.Errorf(`Failed to delete RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
			return -1
		}
		logger.Debugf(`Deleted RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

		// Only delete the parent image if it is a zombie. If it is not
		// we know that LXD is still using it.
		if strings.HasPrefix(volumeType, "zombie_") {
			ret := cephContainerDelete(clusterName, poolName,
				volumeName, volumeType, userName)
			if ret < 0 {
				logger.Errorf(`Failed to delete RBD storage volume "%s"`,
					logImageEntry)
				return -1
			}
			logger.Debugf(`Deleted RBD storage volume "%s"`, logImageEntry)
		}

		return 0
	} else {
		logger.Debugf(`Detected "%v" as clones of RBD snapshot "%s" of RBD storage volume "%s"`, clones, logSnapshotEntry, logImageEntry)

		canDelete := true
		for _, clone := range clones {
			clonePool, cloneType, cloneName, err := parseClone(clone)
			if err != nil {
				logger.Errorf(`Failed to parse clone "%s" of RBD snapshot "%s" of RBD storage volume "%s"`, clone, logSnapshotEntry, logImageEntry)
				return -1
			}
			logger.Debugf(`Split clone "%s" of RBD snapshot "%s" of RBD storage volume "%s" into pool name "%s", volume type "%s", and volume name "%s"`, clone, logSnapshotEntry, logImageEntry, clonePool, cloneType, cloneName)

			if !strings.HasPrefix(cloneType, "zombie_") {
				canDelete = false
				continue
			}

			ret := cephContainerDelete(clusterName, clonePool,
				cloneName, cloneType, userName)
			if ret < 0 {
				logger.Errorf(`Failed to delete clone "%s" of RBD snapshot "%s" of RBD storage volume "%s"`, clone, logSnapshotEntry, logImageEntry)
				return -1
			} else if ret == 1 {
				// Only marked as zombie
				canDelete = false
			}
		}

		if canDelete {
			logger.Debugf(`Deleted all clones of RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			// unprotect
			err = cephRBDSnapshotUnprotect(clusterName, poolName,
				volumeName, volumeType, snapshotName, userName)
			if err != nil {
				logger.Errorf(`Failed to unprotect RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
				return -1
			}
			logger.Debugf(`Unprotected RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			// unmap
			err = cephRBDVolumeSnapshotUnmap(clusterName, poolName,
				volumeName, volumeType, snapshotName, userName,
				true)
			if err != nil {
				logger.Errorf(`Failed to unmap RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
				return -1
			}
			logger.Debugf(`Unmapped RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			// delete
			err = cephRBDSnapshotDelete(clusterName, poolName,
				volumeName, volumeType, snapshotName, userName)
			if err != nil {
				logger.Errorf(`Failed to delete RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
				return -1
			}
			logger.Debugf(`Deleted RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			// Only delete the parent image if it is a zombie. If it
			// is not we know that LXD is still using it.
			if strings.HasPrefix(volumeType, "zombie_") {
				ret := cephContainerDelete(clusterName,
					poolName, volumeName, volumeType,
					userName)
				if ret < 0 {
					logger.Errorf(`Failed to delete RBD storage volume "%s"`, logImageEntry)
					return -1
				}
				logger.Debugf(`Deleted RBD storage volume "%s"`,
					logImageEntry)
			}
		} else {
			logger.Debugf(`Could not delete all clones of RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			if strings.HasPrefix(snapshotName, "zombie_") {
				return 1
			}

			err := cephRBDVolumeSnapshotUnmap(clusterName, poolName,
				volumeName, volumeType, snapshotName, userName,
				true)
			if err != nil {
				logger.Errorf(`Failed to unmap RBD snapshot "%s" of RBD storage volume "%s": %s`, logSnapshotEntry, logImageEntry, err)
				return -1
			}
			logger.Debug(`Unmapped RBD snapshot "%s" of RBD storage volume "%s"`, logSnapshotEntry, logImageEntry)

			newSnapshotName := fmt.Sprintf("zombie_%s", snapshotName)
			logSnapshotNewEntry := fmt.Sprintf("%s/%s_%s@%s",
				poolName, volumeName, volumeType, newSnapshotName)
			err = cephRBDVolumeSnapshotRename(clusterName, poolName,
				volumeName, volumeType, snapshotName,
				newSnapshotName, userName)
			if err != nil {
				logger.Errorf(`Failed to rename RBD snapshot "%s" of RBD storage volume "%s" to %s`, logSnapshotEntry, logImageEntry, logSnapshotNewEntry)
				return -1
			}
			logger.Debugf(`Renamed RBD snapshot "%s" of RBD storage volume "%s" to %s`, logSnapshotEntry, logImageEntry, logSnapshotNewEntry)
		}

	}

	return 1
}

// parseParent splits a string describing a RBD storage entity into its
// components
// This can be used on strings like
// <osd-pool-name>/<lxd-specific-prefix>_<rbd-storage-volume>@<rbd-snapshot-name>
// and will split it into
// <osd-pool-name>, <rbd-storage-volume>, <lxd-specific-prefix>, <rbdd-snapshot-name>
func parseParent(parent string) (string, string, string, string, error) {
	idx := strings.Index(parent, "/")
	if idx == -1 {
		return "", "", "", "", fmt.Errorf("Unexpected parsing error")
	}
	slider := parent[(idx + 1):]
	poolName := parent[:idx]

	volumeType := slider
	idx = strings.Index(slider, "zombie_")
	if idx == 0 {
		idx += len("zombie_")
		volumeType = slider
		slider = slider[idx:]
	}

	idxType := strings.Index(slider, "_")
	if idxType == -1 {
		return "", "", "", "", fmt.Errorf("Unexpected parsing error")
	}

	if idx == len("zombie_") {
		idxType += idx
	}
	volumeType = volumeType[:idxType]

	idx = strings.Index(slider, "_")
	if idx == -1 {
		return "", "", "", "", fmt.Errorf("Unexpected parsing error")
	}

	volumeName := slider
	idx = strings.Index(volumeName, "_")
	if idx == -1 {
		return "", "", "", "", fmt.Errorf("Unexpected parsing error")
	}
	volumeName = volumeName[(idx + 1):]

	idx = strings.Index(volumeName, "@")
	if idx == -1 {
		return "", "", "", "", fmt.Errorf("Unexpected parsing error")
	}
	snapshotName := volumeName[(idx + 1):]
	volumeName = volumeName[:idx]

	return poolName, volumeType, volumeName, snapshotName, nil
}

// parseClone splits a strings describing an RBD storage volume
// For example a string like
// <osd-pool-name>/<lxd-specific-prefix>_<rbd-storage-volume>
// will be split into
// <osd-pool-name>, <lxd-specific-prefix>, <rbd-storage-volume>
func parseClone(clone string) (string, string, string, error) {
	idx := strings.Index(clone, "/")
	if idx == -1 {
		return "", "", "", fmt.Errorf("Unexpected parsing error")
	}
	slider := clone[(idx + 1):]
	poolName := clone[:idx]

	volumeType := slider
	idx = strings.Index(slider, "zombie_")
	if idx == 0 {
		idx += len("zombie_")
		volumeType = slider
		slider = slider[idx:]
	}

	idxType := strings.Index(slider, "_")
	if idxType == -1 {
		return "", "", "", fmt.Errorf("Unexpected parsing error")
	}

	if idx == len("zombie_") {
		idxType += idx
	}
	volumeType = volumeType[:idxType]

	idx = strings.Index(slider, "_")
	if idx == -1 {
		return "", "", "", fmt.Errorf("Unexpected parsing error")
	}

	volumeName := slider
	idx = strings.Index(volumeName, "_")
	if idx == -1 {
		return "", "", "", fmt.Errorf("Unexpected parsing error")
	}
	volumeName = volumeName[(idx + 1):]

	return poolName, volumeType, volumeName, nil
}

// getRBDMappedDevPath looks at sysfs to retrieve the device path
// "/dev/rbd<idx>" for an RBD image. If it doesn't find it it will map it if
// told to do so.
func getRBDMappedDevPath(clusterName string, poolName string, volumeType string,
	volumeName string, doMap bool, userName string) (string, int) {
	files, err := ioutil.ReadDir("/sys/devices/rbd")
	if err != nil {
		if os.IsNotExist(err) {
			if doMap {
				goto mapImage
			}

			return "", 0
		}

		return "", -1
	}

	for _, f := range files {
		if !f.IsDir() {
			continue
		}

		fName := f.Name()
		idx, err := strconv.ParseUint(fName, 10, 64)
		if err != nil {
			continue
		}

		tmp := fmt.Sprintf("/sys/devices/rbd/%s/pool", fName)
		contents, err := ioutil.ReadFile(tmp)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return "", -1
		}

		detectedPoolName := strings.TrimSpace(string(contents))
		if detectedPoolName != poolName {
			continue
		}

		tmp = fmt.Sprintf("/sys/devices/rbd/%s/name", fName)
		contents, err = ioutil.ReadFile(tmp)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return "", -1
		}

		typedVolumeName := fmt.Sprintf("%s_%s", volumeType, volumeName)
		detectedVolumeName := strings.TrimSpace(string(contents))
		if detectedVolumeName != typedVolumeName {
			continue
		}

		tmp = fmt.Sprintf("/sys/devices/rbd/%s/snap", fName)
		contents, err = ioutil.ReadFile(tmp)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("/dev/rbd%d", idx), 1
			}

			return "", -1
		}

		detectedSnapName := strings.TrimSpace(string(contents))
		if detectedSnapName != "-" {
			continue
		}

		return fmt.Sprintf("/dev/rbd%d", idx), 1
	}

	if !doMap {
		return "", 0
	}

mapImage:
	devPath, err := cephRBDVolumeMap(clusterName, poolName,
		volumeName, volumeType, userName)
	if err != nil {
		return "", -1
	}

	return strings.TrimSpace(devPath), 2
}

func (s *storageCeph) rbdShrink(path string, size int64, fsType string,
	fsMntPoint string, volumeType int, volumeName string,
	data interface{}) error {
	var err error
	var msg string

	cleanupFunc, err := shrinkVolumeFilesystem(s, volumeType, fsType, path, fsMntPoint, size, data)
	if cleanupFunc != nil {
		defer cleanupFunc()
	}
	if err != nil {
		return err
	}

	volumeTypeName := ""
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		volumeTypeName = storagePoolVolumeTypeNameContainer
	case storagePoolVolumeTypeCustom:
		volumeTypeName = storagePoolVolumeTypeNameCustom
	default:
		return fmt.Errorf(`Resizing not implemented for `+
			`storage volume type %d`, volumeType)
	}
	msg, err = shared.TryRunCommand(
		"rbd",
		"resize",
		"--allow-shrink",
		"--id", s.UserName,
		"--cluster", s.ClusterName,
		"--pool", s.OSDPoolName,
		"--size", fmt.Sprintf("%dM", (size/1024/1024)),
		fmt.Sprintf("%s_%s", volumeTypeName, volumeName))
	if err != nil {
		logger.Errorf(`Could not shrink RBD storage volume "%s": %s`,
			path, msg)
		return fmt.Errorf(`Could not shrink RBD storage volume "%s":
			%s`, path, msg)
	}

	logger.Debugf("Reduce underlying %s filesystem for LV \"%s\"", fsType, path)
	return nil
}

func (s *storageCeph) rbdGrow(path string, size int64, fsType string,
	fsMntPoint string, volumeType int, volumeName string,
	data interface{}) error {

	// Find the volume type name
	volumeTypeName := ""
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		volumeTypeName = storagePoolVolumeTypeNameContainer
	case storagePoolVolumeTypeCustom:
		volumeTypeName = storagePoolVolumeTypeNameCustom
	default:
		return fmt.Errorf(`Resizing not implemented for storage `+
			`volume type %d`, volumeType)
	}

	// Grow the block device
	msg, err := shared.TryRunCommand(
		"rbd",
		"resize",
		"--id", s.UserName,
		"--cluster", s.ClusterName,
		"--pool", s.OSDPoolName,
		"--size", fmt.Sprintf("%dM", (size/1024/1024)),
		fmt.Sprintf("%s_%s", volumeTypeName, volumeName))
	if err != nil {
		logger.Errorf(`Could not extend RBD storage volume "%s": %s`,
			path, msg)
		return fmt.Errorf(`Could not extend RBD storage volume "%s":
			%s`, path, msg)
	}

	// Mount the filesystem
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c := data.(container)
		ourMount, err := c.StorageStart()
		if err != nil {
			return err
		}

		if ourMount {
			defer c.StorageStop()
		}
	case storagePoolVolumeTypeCustom:
		ourMount, err := s.StoragePoolVolumeMount()
		if err != nil {
			return err
		}

		if ourMount {
			defer s.StoragePoolVolumeUmount()
		}
	}

	// Grow the filesystem
	return growFileSystem(fsType, path, fsMntPoint)
}

func parseCephSize(numStr string) (uint64, error) {
	if numStr == "" {
		return 0, fmt.Errorf("Empty string is not valid input")
	}

	lxdSuffix := "GB"
	cephSuffix := numStr[(len(numStr) - 1):]
	switch cephSuffix {
	case "M":
		lxdSuffix = "MB"
	case "K":
		lxdSuffix = "KB"
	}

	_, err := strconv.Atoi(cephSuffix)
	if err != nil {
		numStr = numStr[:(len(numStr) - 1)]
		numStr = strings.TrimSpace(numStr)
	}
	numStr = fmt.Sprintf("%s%s", numStr, lxdSuffix)

	size, err := shared.ParseByteSizeString(numStr)
	if err != nil {
		return 0, err
	}

	return uint64(size), nil
}

// copyWithSnapshots creates a non-sparse copy of a container including its
// snapshots
// This does not introduce a dependency relation between the source RBD storage
// volume and the target RBD storage volume.
func (s *storageCeph) cephRBDVolumeDumpToFile(sourceVolumeName string, file string) error {
	logger.Debugf(`Dumping RBD storage volume "%s" to "%s"`, sourceVolumeName, file)

	args := []string{
		"export",
		"--id", s.UserName,
		"--cluster", s.ClusterName,
		sourceVolumeName,
		file,
	}

	rbdSendCmd := exec.Command("rbd", args...)
	err := rbdSendCmd.Run()
	if err != nil {
		return err
	}

	logger.Debugf(`Dumped RBD storage volume "%s" to "%s"`, sourceVolumeName, file)
	return nil
}

// cephRBDVolumeBackupCreate creates a backup of a container or snapshot.
func (s *storageCeph) cephRBDVolumeBackupCreate(tmpPath string, backup backup, source container) error {
	sourceIsSnapshot := source.IsSnapshot()
	sourceContainerName := source.Name()
	sourceContainerOnlyName := projectPrefix(source.Project(), sourceContainerName)
	sourceSnapshotOnlyName := ""

	// Prepare for rsync
	rsync := func(oldPath string, newPath string, bwlimit string) error {
		output, err := rsyncLocalCopy(oldPath, newPath, bwlimit)
		if err != nil {
			return fmt.Errorf("Failed to rsync: %s: %s", string(output), err)
		}

		return nil
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	// Create a temporary snapshot
	snapshotName := fmt.Sprintf("zombie_snapshot_%s", uuid.NewRandom().String())
	if sourceIsSnapshot {
		sourceContainerOnlyName, sourceSnapshotOnlyName, _ = containerGetParentAndSnapshotName(sourceContainerName)
		sourceContainerOnlyName = projectPrefix(source.Project(), sourceContainerOnlyName)
		snapshotName = fmt.Sprintf("snapshot_%s", projectPrefix(source.Project(), sourceSnapshotOnlyName))
	} else {
		// This is costly but we need to ensure that all cached data has
		// been committed to disk. If we don't then the rbd snapshot of
		// the underlying filesystem can be inconsistent or - worst case
		// - empty.
		syscall.Sync()

		// create snapshot
		err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName, sourceContainerOnlyName, storagePoolVolumeTypeNameContainer, snapshotName, s.UserName)
		if err != nil {
			return err
		}
		defer cephRBDSnapshotDelete(s.ClusterName, s.OSDPoolName, sourceContainerOnlyName, storagePoolVolumeTypeNameContainer, snapshotName, s.UserName)
	}

	// Protect volume so we can create clones of it
	err := cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName, sourceContainerOnlyName, storagePoolVolumeTypeNameContainer, snapshotName, s.UserName)
	if err != nil {
		return err
	}
	defer cephRBDSnapshotUnprotect(s.ClusterName, s.OSDPoolName, sourceContainerOnlyName, storagePoolVolumeTypeNameContainer, snapshotName, s.UserName)

	// Create a new volume from the snapshot
	cloneName := uuid.NewRandom().String()
	err = cephRBDCloneCreate(s.ClusterName, s.OSDPoolName, sourceContainerOnlyName, storagePoolVolumeTypeNameContainer, snapshotName, s.OSDPoolName, cloneName, "backup", s.UserName)
	if err != nil {
		return err
	}
	defer cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName, cloneName, "backup", s.UserName)

	// Map the new volume
	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, cloneName, "backup", s.UserName)
	if err != nil {
		return err
	}
	defer cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, cloneName, "backup", s.UserName, true)

	// Generate a new UUID if needed
	RBDFilesystem := s.getRBDFilesystem()
	msg, err := fsGenerateNewUUID(RBDFilesystem, RBDDevPath)
	if err != nil {
		logger.Errorf("Failed to create new UUID for filesystem \"%s\": %s: %s", RBDFilesystem, msg, err)
		return err
	}

	// Create a temporary mountpoing
	tmpContainerMntPoint, err := ioutil.TempDir("", "lxd_backup_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	// Mount the volume
	mountFlags, mountOptions := lxdResolveMountoptions(s.getRBDMountOptions())
	err = tryMount(RBDDevPath, tmpContainerMntPoint, RBDFilesystem, mountFlags, mountOptions)
	if err != nil {
		logger.Errorf("Failed to mount RBD device %s onto %s: %s", RBDDevPath, tmpContainerMntPoint, err)
		return err
	}
	logger.Debugf("Mounted RBD device %s onto %s", RBDDevPath, tmpContainerMntPoint)
	defer tryUnmount(tmpContainerMntPoint, syscall.MNT_DETACH)

	// Figure out the target name
	targetName := sourceContainerName
	if sourceIsSnapshot {
		_, targetName, _ = containerGetParentAndSnapshotName(sourceContainerName)
	}

	// Create the path for the backup.
	targetBackupMntPoint := fmt.Sprintf("%s/container", tmpPath)
	if sourceIsSnapshot {
		targetBackupMntPoint = fmt.Sprintf("%s/snapshots/%s", tmpPath, targetName)
	}

	err = os.MkdirAll(targetBackupMntPoint, 0711)
	if err != nil {
		return err
	}

	err = rsync(tmpContainerMntPoint, targetBackupMntPoint, bwlimit)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageCeph) doContainerCreate(project, name string, privileged bool) error {
	logger.Debugf(`Creating RBD storage volume for container "%s" on storage pool "%s"`, name, s.pool.Name)

	revert := true

	// get size
	RBDSize, err := s.getRBDSize()
	if err != nil {
		logger.Errorf(`Failed to retrieve size of RBD storage volume for container "%s" on storage pool "%s": %s`, name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Retrieved size "%s" of RBD storage volume for container "%s" on storage pool "%s"`, RBDSize, name, s.pool.Name)

	// create volume
	volumeName := projectPrefix(project, name)
	err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName, volumeName, storagePoolVolumeTypeNameContainer, RBDSize, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to create RBD storage volume for container "%s" on storage pool "%s": %s`, name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created RBD storage volume for container "%s" on storage pool "%s"`, name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName, volumeName, storagePoolVolumeTypeNameContainer, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD storage volume for container "%s" on storage pool "%s": %s`, name, s.pool.Name, err)
		}
	}()

	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, volumeName, storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for container "%s" on storage pool "%s": %s`, name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Mapped RBD storage volume for container "%s" on storage pool "%s"`, name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, volumeName, storagePoolVolumeTypeNameContainer, s.UserName, true)
		if err != nil {
			logger.Warnf(`Failed to unmap RBD storage volume for container "%s" on storage pool "%s": %s`, name, s.pool.Name, err)
		}
	}()

	// get filesystem
	RBDFilesystem := s.getRBDFilesystem()
	msg, err := makeFSType(RBDDevPath, RBDFilesystem, nil)
	if err != nil {
		logger.Errorf(`Failed to create filesystem type "%s" on device path "%s" for RBD storage volume for container "%s" on storage pool "%s": %s`, RBDFilesystem, RBDDevPath, name, s.pool.Name, msg)
		return err
	}
	logger.Debugf(`Created filesystem type "%s" on device path "%s" for RBD storage volume for container "%s" on storage pool "%s"`, RBDFilesystem, RBDDevPath, name, s.pool.Name)

	containerPath := shared.VarPath("containers", projectPrefix(project, name))
	containerMntPoint := getContainerMountPoint(project, s.pool.Name, name)
	err = createContainerMountpoint(containerMntPoint, containerPath, privileged)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s" for RBD storage volume for container "%s" on storage pool "%s": %s"`, containerMntPoint, name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s" for RBD storage volume for container "%s" on storage pool "%s""`, containerMntPoint, name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := os.Remove(containerMntPoint)
		if err != nil {
			logger.Warnf(`Failed to delete mountpoint "%s" for RBD storage volume for container "%s" on storage pool "%s": %s"`, containerMntPoint, name, s.pool.Name, err)
		}
	}()

	logger.Debugf(`Created RBD storage volume for container "%s" on storage pool "%s"`, name, s.pool.Name)

	revert = false

	return nil
}

func (s *storageCeph) doContainerMount(project string, name string) (bool, error) {
	RBDFilesystem := s.getRBDFilesystem()
	containerMntPoint := getContainerMountPoint(project, s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(project, s.pool.Name, name)
	}

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		logger.Debugf("RBD storage volume for container \"%s\" on storage pool \"%s\" appears to be already mounted", s.volume.Name, s.pool.Name)
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var ret int
	var mounterr error
	ourMount := false
	RBDDevPath := ""
	if !shared.IsMountPoint(containerMntPoint) {
		volumeName := projectPrefix(project, name)
		RBDDevPath, ret = getRBDMappedDevPath(s.ClusterName,
			s.OSDPoolName, storagePoolVolumeTypeNameContainer,
			volumeName, true, s.UserName)
		if ret >= 0 {
			mountFlags, mountOptions := lxdResolveMountoptions(s.getRBDMountOptions())
			mounterr = tryMount(RBDDevPath, containerMntPoint,
				RBDFilesystem, mountFlags, mountOptions)
			ourMount = true
		}
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if mounterr != nil || ret < 0 {
		logger.Errorf("Failed to mount RBD storage volume for container \"%s\": %s", s.volume.Name, mounterr)
		return false, mounterr
	}

	return ourMount, nil
}

func (s *storageCeph) doContainerSnapshotCreate(project, targetName string, sourceName string) error {
	logger.Debugf(`Creating RBD storage volume for snapshot "%s" on storage pool "%s"`, targetName, s.pool.Name)

	revert := true

	_, targetSnapshotOnlyName, _ := containerGetParentAndSnapshotName(targetName)
	targetSnapshotName := fmt.Sprintf("snapshot_%s", targetSnapshotOnlyName)
	err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName,
		projectPrefix(project, sourceName), storagePoolVolumeTypeNameContainer,
		targetSnapshotName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to create snapshot for RBD storage volume for snapshot "%s" on storage pool "%s": %s`, targetName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created snapshot for RBD storage volume for image "%s" on storage pool "%s"`, targetName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDSnapshotDelete(s.ClusterName, s.OSDPoolName,
			sourceName, storagePoolVolumeTypeNameContainer,
			targetSnapshotName, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD container storage for snapshot "%s" of container "%s"`, targetSnapshotOnlyName, sourceName)
		}
	}()

	targetContainerMntPoint := getSnapshotMountPoint(project, s.pool.Name, targetName)
	sourceOnlyName, _, _ := containerGetParentAndSnapshotName(sourceName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(project, sourceOnlyName))
	snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(project, sourceOnlyName))
	err = createSnapshotMountpoint(targetContainerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s", snapshot symlink target "%s", snapshot mountpoint symlink"%s" for RBD storage volume "%s" on storage pool "%s": %s`, targetContainerMntPoint, snapshotMntPointSymlinkTarget,
			snapshotMntPointSymlink, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s", snapshot symlink target "%s", snapshot mountpoint symlink"%s" for RBD storage volume "%s" on storage pool "%s"`, targetContainerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, s.volume.Name, s.pool.Name)

	logger.Debugf(`Created RBD storage volume for snapshot "%s" on storage pool "%s"`, targetName, s.pool.Name)

	revert = false

	return nil
}
