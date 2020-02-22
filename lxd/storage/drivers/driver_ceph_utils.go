package drivers

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// osdPoolExists checks whether a given OSD pool exists.
func (d *ceph) osdPoolExists() bool {
	_, err := shared.RunCommand(
		"ceph",
		"--name", fmt.Sprintf("client.%s", d.config["ceph.user.name"]),
		"--cluster", d.config["ceph.cluster_name"],
		"osd",
		"pool",
		"get",
		d.config["ceph.osd.pool_name"],
		"size")

	return err == nil
}

// osdDeletePool destroys an OSD pool.
// - A call to osdDeletePool will destroy a pool including any storage
//   volumes that still exist in the pool.
// - In case the OSD pool that is supposed to be deleted does not exist this
//   command will still exit 0. This means that if the caller wants to be sure
//   that this call actually deleted an OSD pool it needs to check for the
//   existence of the pool first.
func (d *ceph) osdDeletePool() error {
	_, err := shared.RunCommand("ceph",
		"--name", fmt.Sprintf("client.%s", d.config["ceph.user.name"]),
		"--cluster", d.config["ceph.cluster_name"],
		"osd",
		"pool",
		"delete",
		d.config["ceph.osd.pool_name"],
		d.config["ceph.osd.pool_name"],
		"--yes-i-really-really-mean-it")
	if err != nil {
		return err
	}

	return nil
}

// rbdCreateVolume creates an RBD storage volume.
// Note that the set of features is intentionally limited is intentionally
// limited by passing --image-feature explicitly. This is done to ensure that
// the chances of a conflict between the features supported by the userspace
// library and the kernel module are minimized. Otherwise random panics might
// occur.
func (d *ceph) rbdCreateVolume(vol Volume, size string) error {
	cmd := []string{
		"--id", d.config["ceph.user.name"],
		"--image-feature", "layering,",
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
	}

	if d.config["ceph.osd.data_pool_name"] != "" {
		cmd = append(cmd, "--data-pool", d.config["ceph.osd.data_pool_name"])
	}

	cmd = append(cmd,
		"--size", size,
		"create",
		d.getRBDVolumeName(vol, "", false, false))

	_, err := shared.RunCommand("rbd", cmd...)
	return err
}

// rbdDeleteVolume deletes an RBD storage volume.
// - In case the RBD storage volume that is supposed to be deleted does not
//   exist this command will still exit 0. This means that if the caller wants
//   to be sure that this call actually deleted an RBD storage volume it needs
//   to check for the existence of the pool first.
func (d *ceph) rbdDeleteVolume(vol Volume) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"rm",
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		return err
	}

	return nil
}

// rbdMapVolume maps a given RBD storage volume.
// This will ensure that the RBD storage volume is accessible as a block device
// in the /dev directory and is therefore necessary in order to mount it.
func (d *ceph) rbdMapVolume(vol Volume) (string, error) {
	devPath, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"map",
		d.getRBDVolumeName(vol, "", false, false))
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

// rbdUnmapVolume unmaps a given RBD storage volume.
// This is a precondition in order to delete an RBD storage volume can.
func (d *ceph) rbdUnmapVolume(vol Volume, unmapUntilEINVAL bool) error {
	busyCount := 0

again:
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"unmap",
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EINVAL (already unmapped).
					return nil
				}

				if waitStatus.ExitStatus() == 16 {
					// EBUSY (currently in use).
					busyCount++
					if busyCount == 10 {
						return err
					}

					// Wait a second an try again.
					time.Sleep(time.Second)
					goto again
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

// rbdUnmapVolumeSnapshot unmaps a given RBD snapshot.
// This is a precondition in order to delete an RBD snapshot can.
func (d *ceph) rbdUnmapVolumeSnapshot(vol Volume, snapshotName string, unmapUntilEINVAL bool) error {
again:
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"unmap",
		d.getRBDVolumeName(vol, snapshotName, false, false))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EINVAL (already unmapped).
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

// rbdCreateVolumeSnapshot creates a read-write snapshot of a given RBD storage volume.
func (d *ceph) rbdCreateVolumeSnapshot(vol Volume, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"create",
		"--snap", snapshotName,
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		return err
	}

	return nil
}

// rbdProtectVolumeSnapshot protects a given snapshot from being deleted.
// This is a precondition to be able to create RBD clones from a given snapshot.
func (d *ceph) rbdProtectVolumeSnapshot(vol Volume, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"protect",
		"--snap", snapshotName,
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 16 {
					// EBUSY (snapshot already protected).
					return nil
				}
			}
		}
		return err
	}

	return nil
}

// rbdUnprotectVolumeSnapshot unprotects a given snapshot.
// - This is a precondition to be able to delete an RBD snapshot.
// - This command will only succeed if the snapshot does not have any clones.
func (d *ceph) rbdUnprotectVolumeSnapshot(vol Volume, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"unprotect",
		"--snap", snapshotName,
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EBUSY (snapshot already unprotected).
					return nil
				}
			}
		}
		return err
	}

	return nil
}

// rbdCreateClone creates a clone from a protected RBD snapshot.
func (d *ceph) rbdCreateClone(sourceVol Volume, sourceSnapshotName string, targetVol Volume) error {
	cmd := []string{
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--image-feature", "layering",
	}

	if d.config["ceph.osd.data_pool_name"] != "" {
		cmd = append(cmd, "--data-pool", d.config["ceph.osd.data_pool_name"])
	}

	cmd = append(cmd,
		"clone",
		d.getRBDVolumeName(sourceVol, sourceSnapshotName, false, true),
		d.getRBDVolumeName(targetVol, "", false, true))

	_, err := shared.RunCommand("rbd", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// rbdListSnapshotClones list all clones of an RBD snapshot.
func (d *ceph) rbdListSnapshotClones(vol Volume, snapshotName string) ([]string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"children",
		"--image", d.getRBDVolumeName(vol, "", false, false),
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

// rbdMarkVolumeDeleted marks an RBD storage volume as being in "zombie" state.
// An RBD storage volume that is in zombie state is not tracked in LXD's
// database anymore but still needs to be kept around for the sake of any
// dependent storage entities in the storage pool. This usually happens when an
// RBD storage volume has protected snapshots; a scenario most common when
// creating a sparse copy of a container or when LXD updated an image and the
// image still has dependent container clones.
func (d *ceph) rbdMarkVolumeDeleted(vol Volume, newVolumeName string) error {
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newVolumeName, nil, nil)
	deletedName := d.getRBDVolumeName(newVol, "", true, true)
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"mv",
		d.getRBDVolumeName(vol, "", false, true),
		deletedName)
	if err != nil {
		return err
	}

	return nil
}

// rbdRenameVolume renames a given RBD storage volume.
// Note that this usually requires that the image be unmapped under its original
// name, then renamed, and finally will be remapped again. If it is not unmapped
// under its original name and the callers maps it under its new name the image
// will be mapped twice. This will prevent it from being deleted.
func (d *ceph) rbdRenameVolume(vol Volume, newVolumeName string) error {
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newVolumeName, nil, nil)

	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"mv",
		d.getRBDVolumeName(vol, "", false, true),
		d.getRBDVolumeName(newVol, "", false, true))
	if err != nil {
		return err
	}

	return nil
}

// rbdRenameVolumeSnapshot renames a given RBD storage volume.
// Note that if the snapshot is mapped - which it usually shouldn't be - this
// usually requires that the snapshot be unmapped under its original name, then
// renamed, and finally will be remapped again. If it is not unmapped under its
// original name and the caller maps it under its new name the snapshot will be
// mapped twice. This will prevent it from being deleted.
func (d *ceph) rbdRenameVolumeSnapshot(vol Volume, oldSnapshotName string, newSnapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"snap",
		"rename",
		d.getRBDVolumeName(vol, oldSnapshotName, false, true),
		d.getRBDVolumeName(vol, newSnapshotName, false, true))
	if err != nil {
		return err
	}

	return nil
}

// rbdGetVolumeParent will return the snapshot the RBD clone was created from:
// - If the RBD storage volume is not a clone then this function will return
//   db.NoSuchObjectError.
// - The snapshot will be returned as
//   <osd-pool-name>/<rbd-volume-name>@<rbd-snapshot-name>
//   The caller will usually want to parse this according to its needs. This
//   helper library provides two small functions to do this but see below.
func (d *ceph) rbdGetVolumeParent(vol Volume) (string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"info",
		d.getRBDVolumeName(vol, "", false, false))
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

// rbdDeleteVolumeSnapshot deletes an RBD snapshot.
// This requires that the snapshot does not have any clones and is unmapped and
// unprotected.
func (d *ceph) rbdDeleteVolumeSnapshot(vol Volume, snapshotName string) error {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"rm",
		d.getRBDVolumeName(vol, snapshotName, false, false))
	if err != nil {
		return err
	}

	return nil
}

// rbdListVolumeSnapshots retrieves the snapshots of an RBD storage volume.
// The format of the snapshot names is simply the part after the @. So given a
// valid RBD path relative to a pool
// <osd-pool-name>/<rbd-storage-volume>@<rbd-snapshot-name>
// this will only return
// <rbd-snapshot-name>
func (d *ceph) rbdListVolumeSnapshots(vol Volume) ([]string, error) {
	msg, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--format", "json",
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"ls",
		d.getRBDVolumeName(vol, "", false, false))
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

// getRBDSize returns the size the RBD storage volume is supposed to be created with.
func (d *ceph) getRBDSize(vol Volume) (string, error) {
	size, ok := vol.config["size"]
	if !ok {
		size = vol.poolConfig["volume.size"]
	}

	sz, err := units.ParseByteSizeString(size)
	if err != nil {
		return "", err
	}

	// Safety net: Set to default value.
	if sz == 0 {
		sz, _ = units.ParseByteSizeString("10GB")
	}

	return fmt.Sprintf("%dB", sz), nil
}

// getRBDFilesystem returns the filesystem the RBD storage volume is supposed to be created with.
func (d *ceph) getRBDFilesystem(vol Volume) string {
	if vol.config["block.filesystem"] != "" {
		return vol.config["block.filesystem"]
	}

	if vol.poolConfig["volume.block.filesystem"] != "" {
		return vol.poolConfig["volume.block.filesystem"]
	}

	return "ext4"
}

// getRBDMountOptions returns the mount options the storage volume is supposed to be mounted with
// the option string that is returned needs to be passed to the approriate
// helper (currently named "LXDResolveMountoptions") which will take on the job
// of splitting it into appropriate flags and string options.
func (d *ceph) getRBDMountOptions(vol Volume) string {
	if vol.config["block.mount_options"] != "" {
		return vol.config["block.mount_options"]
	}

	if vol.poolConfig["volume.block.mount_options"] != "" {
		return vol.poolConfig["volume.block.mount_options"]
	}

	if d.getRBDFilesystem(vol) == "btrfs" {
		return "user_subvol_rm_allowed,discard"
	}

	return "discard"
}

// copyWithSnapshots creates a non-sparse copy of a container including its snapshots.
// This does not introduce a dependency relation between the source RBD storage
// volume and the target RBD storage volume.
func (d *ceph) copyWithSnapshots(sourceVolumeName string, targetVolumeName string, sourceParentSnapshot string) error {
	args := []string{
		"export-diff",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		sourceVolumeName,
	}

	if sourceParentSnapshot != "" {
		args = append(args, "--from-snap", sourceParentSnapshot)
	}

	// Redirect output to stdout.
	args = append(args, "-")

	rbdSendCmd := exec.Command("rbd", args...)
	rbdRecvCmd := exec.Command(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"import-diff",
		"--cluster", d.config["ceph.cluster_name"],
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

	return nil
}

// deleteVolume deletes the RBD storage volume of a container including any dependencies.
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
// - deleteVolume in conjunction with deleteVolumeSnapshot
//   recurses through an OSD storage pool to find and delete any storage
//   entities that were kept around because of dependency relations but are not
//   deletable.
func (d *ceph) deleteVolume(vol Volume) (int, error) {
	snaps, err := d.rbdListVolumeSnapshots(vol)
	if err == nil {
		var zombies int
		for _, snap := range snaps {
			ret, err := d.deleteVolumeSnapshot(vol, snap)
			if ret < 0 {
				return -1, err
			} else if ret == 1 {
				zombies++
			}
		}

		if zombies > 0 {
			// Unmap.
			err = d.rbdUnmapVolume(vol, true)
			if err != nil {
				return -1, err
			}

			if strings.HasPrefix(vol.name, "zombie_") ||
				strings.HasPrefix(string(vol.volType), "zombie_") {
				return 1, nil
			}

			newVolumeName := fmt.Sprintf("%s_%s", vol.name, uuid.NewRandom().String())
			err := d.rbdMarkVolumeDeleted(vol, newVolumeName)
			if err != nil {
				return -1, err
			}

			return 1, nil
		}
	} else {
		if err != db.ErrNoSuchObject {
			return -1, err
		}

		parent, err := d.rbdGetVolumeParent(vol)
		if err == nil {
			_, parentVolumeType, parentVolumeName,
				parentSnapshotName, err := d.parseParent(parent)
			if err != nil {
				return -1, err
			}

			// Unmap.
			err = d.rbdUnmapVolume(vol, true)
			if err != nil {
				return -1, err
			}

			// Delete.
			err = d.rbdDeleteVolume(vol)
			if err != nil {
				return -1, err
			}

			parentVol := NewVolume(d, d.name, VolumeType(parentVolumeType), vol.contentType, parentVolumeName, nil, nil)

			// Only delete the parent snapshot of the container if
			// it is a zombie. If it is not we know that LXD is
			// still using it.
			if strings.HasPrefix(parentVolumeType, "zombie_") ||
				strings.HasPrefix(parentSnapshotName, "zombie_") {
				ret, err := d.deleteVolumeSnapshot(parentVol, parentSnapshotName)
				if ret < 0 {
					return -1, err
				}
			}
		} else {
			if err != db.ErrNoSuchObject {
				return -1, err
			}

			// Unmap.
			err = d.rbdUnmapVolume(vol, true)
			if err != nil {
				return -1, err
			}

			// Delete.
			err = d.rbdDeleteVolume(vol)
			if err != nil {
				return -1, err
			}
		}
	}

	return 0, nil
}

// deleteVolumeSnapshot deletes an RBD snapshot of a container including any dependencies.
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
// - deleteVolumeSnapshot in conjunction with deleteVolume
//   recurses through an OSD storage pool to find and delete any storage
//   entities that were kept around because of dependency relations but are not
//   deletable.
func (d *ceph) deleteVolumeSnapshot(vol Volume, snapshotName string) (int, error) {
	clones, err := d.rbdListSnapshotClones(vol, snapshotName)
	if err != nil {
		if err != db.ErrNoSuchObject {
			return -1, err
		}

		// Unprotect.
		err = d.rbdUnprotectVolumeSnapshot(vol, snapshotName)
		if err != nil {
			return -1, err
		}

		// Unmap.
		err = d.rbdUnmapVolumeSnapshot(vol, snapshotName, true)
		if err != nil {
			return -1, err
		}

		// Delete.
		err = d.rbdDeleteVolumeSnapshot(vol, snapshotName)
		if err != nil {
			return -1, err
		}

		// Only delete the parent image if it is a zombie. If it is not
		// we know that LXD is still using it.
		if strings.HasPrefix(string(vol.volType), "zombie_") {
			ret, err := d.deleteVolume(vol)
			if ret < 0 {
				return -1, err
			}
		}

		return 0, nil
	}

	canDelete := true
	for _, clone := range clones {
		_, cloneType, cloneName, err := d.parseClone(clone)
		if err != nil {
			return -1, err
		}

		if !strings.HasPrefix(cloneType, "zombie_") {
			canDelete = false
			continue
		}

		cloneVol := NewVolume(d, d.name, VolumeType(cloneType), vol.contentType, cloneName, nil, nil)

		ret, err := d.deleteVolume(cloneVol)
		if ret < 0 {
			return -1, err
		} else if ret == 1 {
			// Only marked as zombie.
			canDelete = false
		}
	}

	if canDelete {
		// Unprotect.
		err = d.rbdUnprotectVolumeSnapshot(vol, snapshotName)
		if err != nil {
			return -1, err
		}

		// Unmap.
		err = d.rbdUnmapVolumeSnapshot(vol, snapshotName,
			true)
		if err != nil {
			return -1, err
		}

		// Delete.
		err = d.rbdDeleteVolumeSnapshot(vol, snapshotName)
		if err != nil {
			return -1, err
		}

		// Only delete the parent image if it is a zombie. If it
		// is not we know that LXD is still using it.
		if strings.HasPrefix(string(vol.volType), "zombie_") {
			ret, err := d.deleteVolume(vol)
			if ret < 0 {
				return -1, err
			}
		}
	} else {
		if strings.HasPrefix(snapshotName, "zombie_") {
			return 1, nil
		}

		err := d.rbdUnmapVolumeSnapshot(vol, snapshotName, true)
		if err != nil {
			return -1, err
		}

		newSnapshotName := fmt.Sprintf("zombie_snapshot_%s", uuid.NewRandom().String())
		err = d.rbdRenameVolumeSnapshot(vol, snapshotName, newSnapshotName)
		if err != nil {
			return -1, err
		}
	}

	return 1, nil
}

// parseParent splits a string describing a RBD storage entity into its components.
// This can be used on strings like
// <osd-pool-name>/<lxd-specific-prefix>_<rbd-storage-volume>@<rbd-snapshot-name>
// and will split it into
// <osd-pool-name>, <rbd-storage-volume>, <lxd-specific-prefix>, <rbdd-snapshot-name>
func (d *ceph) parseParent(parent string) (string, string, string, string, error) {
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

// parseClone splits a strings describing an RBD storage volume.
// For example a string like
// <osd-pool-name>/<lxd-specific-prefix>_<rbd-storage-volume>
// will be split into
// <osd-pool-name>, <lxd-specific-prefix>, <rbd-storage-volume>
func (d *ceph) parseClone(clone string) (string, string, string, error) {
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

// getRBDMappedDevPath looks at sysfs to retrieve the device path.
// "/dev/rbd<idx>" for an RBD image. If it doesn't find it it will map it if
// told to do so.
func (d *ceph) getRBDMappedDevPath(vol Volume, doMap bool) (string, int) {
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

		if detectedPoolName != d.config["ceph.osd.pool_name"] {
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

		typedVolumeName := d.getRBDVolumeName(vol, "", false, false)
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
	devPath, err := d.rbdMapVolume(vol)
	if err != nil {
		return "", -1
	}

	return strings.TrimSpace(devPath), 2
}

// generateUUID regenerates the XFS/btrfs UUID as needed.
func (d *ceph) generateUUID(vol Volume) error {
	fsType := d.getRBDFilesystem(vol)

	if !renegerateFilesystemUUIDNeeded(fsType) {
		return nil
	}

	// Map the RBD volume.
	RBDDevPath, err := d.rbdMapVolume(vol)
	if err != nil {
		return err
	}
	defer d.rbdUnmapVolume(vol, true)

	// Update the UUID.
	err = regenerateFilesystemUUID(fsType, RBDDevPath)
	if err != nil {
		return err
	}

	return nil
}

func (d *ceph) getRBDVolumeName(vol Volume, snapName string, zombie bool, withPoolName bool) string {
	out := ""
	volumeType := string(vol.volType)
	parentName, snapshotName, isSnapshot := shared.InstanceGetParentAndSnapshotName(vol.name)

	if vol.volType == VolumeTypeImage {
		parentName = fmt.Sprintf("%s_%s", parentName, d.getRBDFilesystem(vol))
	}

	if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeBlock {
		parentName = fmt.Sprintf("%s.block", parentName)
	}

	switch vol.volType {
	case VolumeTypeContainer:
		volumeType = db.StoragePoolVolumeTypeNameContainer
	case VolumeTypeVM:
		volumeType = db.StoragePoolVolumeTypeNameVM
	case VolumeTypeImage:
		volumeType = db.StoragePoolVolumeTypeNameImage
	case VolumeTypeCustom:
		volumeType = db.StoragePoolVolumeTypeNameCustom
	}

	if snapName != "" {
		// Always use the provided snapshot name if specified.
		out = fmt.Sprintf("%s_%s@%s", volumeType, parentName, snapName)
	} else {
		if isSnapshot {
			// If volumeName is a snapshot (<vol>/<snap>) and snapName is not set,
			// assume that it's a normal snapshot (not a zombie) and prefix it with
			// "snapshot_".
			out = fmt.Sprintf("%s_%s@snapshot_%s", volumeType, parentName, snapshotName)
		} else {
			out = fmt.Sprintf("%s_%s", volumeType, parentName)
		}
	}

	// If the volume is to be in zombie state (i.e. not tracked by the LXD database),
	// prefix the output with "zombie_".
	if zombie {
		out = fmt.Sprintf("zombie_%s", out)
	}

	// If needed, the output will be prefixed with the pool name, e.g.
	// <pool>/<type>_<volname>@<snapname>.
	if withPoolName {
		out = fmt.Sprintf("%s/%s", d.config["ceph.osd.pool_name"], out)
	}

	return out
}

// Let's say we want to send the a container "a" including snapshots "snap0" and
// "snap1" on storage pool "pool1" from LXD "l1" to LXD "l2" on storage pool
// "pool2":
//
// The pool layout on "l1" would be:
//	pool1/container_a
//	pool1/container_a@snapshot_snap0
//	pool1/container_a@snapshot_snap1
//
// Then we need to send:
//	rbd export-diff pool1/container_a@snapshot_snap0 - | rbd import-diff - pool2/container_a
// (Note that pool2/container_a must have been created by the receiving LXD
// instance before.)
//	rbd export-diff pool1/container_a@snapshot_snap1 --from-snap snapshot_snap0 - | rbd import-diff - pool2/container_a
//	rbd export-diff pool1/container_a --from-snap snapshot_snap1 - | rbd import-diff - pool2/container_a
func (d *ceph) sendVolume(conn io.ReadWriteCloser, volumeName string, volumeParentName string, tracker *ioprogress.ProgressTracker) error {
	args := []string{
		"export-diff",
		"--cluster", d.config["ceph.cluster_name"],
		volumeName,
	}

	if volumeParentName != "" {
		args = append(args, "--from-snap", volumeParentName)
	}

	// Redirect output to stdout.
	args = append(args, "-")

	cmd := exec.Command("rbd", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Setup progress tracker.
	stdoutPipe := stdout
	if tracker != nil {
		stdoutPipe = &ioprogress.ProgressReader{
			ReadCloser: stdout,
			Tracker:    tracker,
		}
	}

	// Forward any output on stdout.
	chStdoutPipe := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdoutPipe)
		chStdoutPipe <- err
		conn.Close()
	}()

	err = cmd.Start()
	if err != nil {
		return err
	}

	output, _ := ioutil.ReadAll(stderr)

	// Handle errors.
	errs := []error{}
	chStdoutPipeErr := <-chStdoutPipe

	err = cmd.Wait()
	if err != nil {
		errs = append(errs, err)

		if chStdoutPipeErr != nil {
			errs = append(errs, chStdoutPipeErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("ceph export-diff failed: %v (%s)", errs, string(output))
	}

	return nil
}

func (d *ceph) receiveVolume(volumeName string, conn io.ReadWriteCloser, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
	args := []string{
		"import-diff",
		"--cluster", d.config["ceph.cluster_name"],
		"-",
		volumeName,
	}

	cmd := exec.Command("rbd", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Forward input through stdin.
	chCopyConn := make(chan error, 1)
	go func() {
		_, err = io.Copy(stdin, conn)
		stdin.Close()
		chCopyConn <- err
	}()

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, _ := ioutil.ReadAll(stderr)

	// Handle errors.
	errs := []error{}
	chCopyConnErr := <-chCopyConn

	err = cmd.Wait()
	if err != nil {
		errs = append(errs, err)

		if chCopyConnErr != nil {
			errs = append(errs, chCopyConnErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Problem with ceph import-diff: (%v) %s", errs, string(output))
	}

	return nil
}
