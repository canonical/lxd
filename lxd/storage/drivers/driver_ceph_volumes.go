package drivers

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *ceph) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(vol.MountPath()) })
	}

	zombieImageVol := NewVolume(d, d.name, VolumeType("zombie_image"), vol.contentType,
		fmt.Sprintf("%s_%s", vol.name, d.getRBDFilesystem(vol)), nil, nil)

	// Check if we have a zombie image. If so, restore it otherwise
	// create a new image volume.
	if vol.volType == VolumeTypeImage && d.HasVolume(zombieImageVol) {
		// unmark deleted
		oldName := d.getRBDVolumeName(zombieImageVol, "", false, true)
		newName := d.getRBDVolumeName(vol, "", false, true)

		_, err := shared.RunCommand(
			"rbd",
			"--id", d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"mv",
			oldName,
			newName)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	// get size
	RBDSize, err := d.getRBDSize(vol)
	if err != nil {
		return err
	}

	// create volume
	err = d.rbdCreateVolume(vol, RBDSize)
	if err != nil {
		return err
	}

	revert.Add(func() { d.DeleteVolume(vol, op) })

	RBDDevPath, err := d.rbdMapVolume(vol)
	if err != nil {
		return err
	}

	revert.Add(func() { d.rbdUnmapVolume(vol, true) })

	// get filesystem
	RBDFilesystem := d.getRBDFilesystem(vol)
	_, err = makeFSType(RBDDevPath, RBDFilesystem, nil)
	if err != nil {
		return err
	}

	// For VMs, also create the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolume(fsVol, op) })
	}

	// Run the volume filler function if supplied.
	if filler != nil && filler.Fill != nil {
		err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
			if vol.contentType == ContentTypeFS {
				return filler.Fill(mountPath, "")
			}

			devPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			err = filler.Fill(mountPath, devPath)
			if err != nil {
				return err
			}

			return err
		}, op)
		if err != nil {
			return err
		}
	}

	// Create a readonly snapshot of the image volume which will be used a the
	// clone source for future non-image volumes.
	if vol.volType == VolumeTypeImage {
		err = d.rbdUnmapVolume(vol, true)
		if err != nil {
			return err
		}

		err = d.rbdCreateVolumeSnapshot(vol, "readonly")
		if err != nil {
			return err
		}

		revert.Add(func() { d.deleteVolumeSnapshot(vol, "readonly") })

		err = d.rbdProtectVolumeSnapshot(vol, "readonly")
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *ceph) CreateVolumeFromBackup(vol Volume, snapshots []string, srcData io.ReadSeeker, optimizedStorage bool, op *operations.Operation) (func(vol Volume) error, func(), error) {
	return genericBackupUnpack(d, vol, snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *ceph) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	var err error
	snapshots := []string{}

	revert := revert.New()
	defer revert.Fail()

	if !srcVol.IsSnapshot() && copySnapshots {
		snapshots, err = d.VolumeSnapshots(srcVol, op)
		if err != nil {
			return err
		}
	}

	// Copy without snapshots
	if !copySnapshots || len(snapshots) == 0 {
		if d.config["ceph.rbd.clone_copy"] != "" &&
			!shared.IsTrue(d.config["ceph.rbd.clone_copy"]) &&
			srcVol.volType != VolumeTypeImage {
			_, err = shared.RunCommand(
				"rbd",
				"--id", d.config["ceph.user.name"],
				"--cluster", d.config["ceph.cluster_name"],
				"cp",
				d.getRBDVolumeName(srcVol, "", false, true),
				d.getRBDVolumeName(vol, "", false, true))
			if err != nil {
				return err
			}

			revert.Add(func() { d.DeleteVolume(vol, op) })

			_, err = d.rbdMapVolume(vol)
			if err != nil {
				return err
			}

			revert.Add(func() { d.rbdUnmapVolume(vol, true) })
		} else {
			parentVol := srcVol
			snapshotName := "readonly"

			if srcVol.volType != VolumeTypeImage {
				snapshotName = fmt.Sprintf("zombie_snapshot_%s", uuid.NewRandom().String())

				if srcVol.IsSnapshot() {
					srcParentName, srcSnapOnlyName, _ :=
						shared.InstanceGetParentAndSnapshotName(srcVol.name)
					snapshotName = fmt.Sprintf("snapshot_%s", srcSnapOnlyName)

					parentVol = NewVolume(d, d.name, srcVol.volType, srcVol.contentType, srcParentName, nil, nil)
				} else {
					// create snapshot
					err := d.rbdCreateVolumeSnapshot(srcVol, snapshotName)
					if err != nil {
						return err
					}
				}

				// protect volume so we can create clones of it
				err = d.rbdProtectVolumeSnapshot(parentVol, snapshotName)
				if err != nil {
					return err
				}

				revert.Add(func() { d.rbdUnprotectVolumeSnapshot(parentVol, snapshotName) })
			}

			err = d.rbdCreateClone(parentVol, snapshotName, vol)
			if err != nil {
				return err
			}

			revert.Add(func() { d.DeleteVolume(vol, op) })
		}

		if vol.contentType == ContentTypeFS {
			// Re-generate the UUID
			err = d.generateUUID(vol)
			if err != nil {
				return err
			}
		}

		ourMount, err := d.MountVolume(vol, op)
		if err != nil {
			return err
		}

		if ourMount {
			defer d.UnmountVolume(vol, op)
		}

		// For VMs, also copy the filesystem volume.
		if vol.IsVMBlock() {
			srcFSVol := srcVol.NewVMBlockFilesystemVolume()
			fsVol := vol.NewVMBlockFilesystemVolume()

			err := d.CreateVolumeFromCopy(fsVol, srcFSVol, false, op)
			if err != nil {
				return err
			}
		}

		revert.Success()
		return nil
	}

	// Copy with snapshots
	// create empty dummy volume
	err = d.rbdCreateVolume(vol, "0")
	if err != nil {
		return err
	}

	revert.Add(func() { d.rbdDeleteVolume(vol) })

	// receive over the dummy volume we created above
	targetVolumeName := d.getRBDVolumeName(vol, "", false, true)

	lastSnap := ""

	if len(snapshots) > 0 {
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}
	}

	for i, snap := range snapshots {
		prev := ""
		if i > 0 {
			prev = fmt.Sprintf("snapshot_%s", snapshots[i-1])
		}

		lastSnap = fmt.Sprintf("snapshot_%s", snap)
		sourceVolumeName := d.getRBDVolumeName(srcVol, lastSnap, false, true)

		err = d.copyWithSnapshots(
			sourceVolumeName,
			targetVolumeName,
			prev)
		if err != nil {
			return err
		}

		revert.Add(func() { d.rbdDeleteVolumeSnapshot(vol, snap) })

		snapVol, err := vol.NewSnapshot(snap)
		if err != nil {
			return err
		}

		err = snapVol.EnsureMountPath()
		if err != nil {
			return err
		}
	}

	// copy snapshot
	sourceVolumeName := d.getRBDVolumeName(srcVol, "", false, true)

	err = d.copyWithSnapshots(
		sourceVolumeName,
		targetVolumeName,
		lastSnap)
	if err != nil {
		return err
	}

	// Re-generate the UUID
	err = d.generateUUID(vol)
	if err != nil {
		return err
	}

	ourMount, err := d.MountVolume(vol, op)
	if err != nil {
		return err
	}

	if ourMount {
		defer d.UnmountVolume(vol, op)
	}

	revert.Success()

	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *ceph) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// Handle simple rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC {
		return genericCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RBD {
		return ErrNotSupported
	}

	recvName := d.getRBDVolumeName(vol, "", false, true)

	if !d.HasVolume(vol) {
		err := d.rbdCreateVolume(vol, "0")
		if err != nil {
			return err
		}
	}

	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Handle zfs send/receive migration.
	if len(volTargetArgs.Snapshots) > 0 {
		// Create the parent directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Transfer the snapshots.
		for _, snapName := range volTargetArgs.Snapshots {
			fullSnapshotName := d.getRBDVolumeName(vol, snapName, false, true)
			wrapper := migration.ProgressWriter(op, "fs_progress", fullSnapshotName)

			err = d.receiveVolume(recvName, conn, wrapper)
			if err != nil {
				return err
			}

			snapVol, err := vol.NewSnapshot(snapName)
			if err != nil {
				return err
			}

			err = snapVol.EnsureMountPath()
			if err != nil {
				return err
			}
		}
	}

	defer func() {
		// Delete all migration-send-* snapshots
		snaps, err := d.rbdListVolumeSnapshots(vol)
		if err != nil {
			return
		}

		for _, snap := range snaps {
			if !strings.HasPrefix(snap, "migration-send") {
				continue
			}

			d.rbdDeleteVolumeSnapshot(vol, snap)
		}
	}()

	wrapper := migration.ProgressWriter(op, "fs_progress", vol.name)

	err = d.receiveVolume(recvName, conn, wrapper)
	if err != nil {
		return err
	}

	if volTargetArgs.Live {
		err = d.receiveVolume(recvName, conn, wrapper)
		if err != nil {
			return err
		}
	}

	err = d.generateUUID(vol)
	if err != nil {
		return err
	}

	return nil
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *ceph) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error {
	return genericCopyVolume(d, nil, vol, srcVol, srcSnapshots, true, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *ceph) DeleteVolume(vol Volume, op *operations.Operation) error {
	if vol.volType == VolumeTypeImage {
		// Try to umount but don't fail
		d.UnmountVolume(vol, op)

		// Check if image has dependant snapshots
		_, err := d.rbdListSnapshotClones(vol, "readonly")
		if err != nil {
			if err != db.ErrNoSuchObject {
				return err
			}

			// Unprotect snapshot
			err = d.rbdUnprotectVolumeSnapshot(vol, "readonly")
			if err != nil {
				return err
			}

			// Delete snapshots
			_, err = shared.RunCommand(
				"rbd",
				"--id", d.config["ceph.user.name"],
				"--cluster", d.config["ceph.cluster_name"],
				"--pool", d.config["ceph.osd.pool_name"],
				"snap",
				"purge",
				d.getRBDVolumeName(vol, "", false, false))
			if err != nil {
				return err
			}

			// Unmap image
			err = d.rbdUnmapVolume(vol, true)
			if err != nil {
				return err
			}

			// Delete image
			err = d.rbdDeleteVolume(vol)
		} else {
			err = d.rbdUnmapVolume(vol, true)
			if err != nil {
				return err
			}

			err = d.rbdMarkVolumeDeleted(vol, vol.name, vol.config["block.filesystem"])
		}
		if err != nil {
			return err
		}
	} else {
		if !d.HasVolume(vol) {
			return nil
		}

		_, err := d.UnmountVolume(vol, op)
		if err != nil {
			return err
		}

		_, err = d.deleteVolume(vol)
		if err != nil {
			return errors.Wrap(err, "Failed to delete volume")
		}
	}

	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		err := d.DeleteVolume(fsVol, op)
		if err != nil {
			return err
		}
	}

	mountPath := vol.MountPath()

	if vol.contentType == ContentTypeFS && shared.PathExists(mountPath) {
		err := wipeDirectory(mountPath)
		if err != nil {
			return err
		}

		err = os.Remove(mountPath)
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Failed to remove '%s'", mountPath)
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *ceph) HasVolume(vol Volume) bool {
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"image-meta",
		"list",
		d.getRBDVolumeName(vol, "", false, false))

	return err == nil
}

// ValidateVolume validates the supplied volume config.
func (d *ceph) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	rules := map[string]func(value string) error{
		"block.filesystem":    shared.IsAny,
		"block.mount_options": shared.IsAny,
	}

	return d.validateVolume(vol, rules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *ceph) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	if vol.volType != VolumeTypeCustom {
		return ErrNotSupported
	}

	val, ok := changedConfig["size"]
	if ok {
		err := d.SetVolumeQuota(vol, val, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *ceph) GetVolumeUsage(vol Volume) (int64, error) {
	return -1, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
func (d *ceph) SetVolumeQuota(vol Volume, size string, op *operations.Operation) error {
	fsType := d.getRBDFilesystem(vol)

	RBDDevPath, ret := d.getRBDMappedDevPath(vol, true)
	if ret < 0 {
		return fmt.Errorf("Failed to get mapped RBD path")
	}

	oldSize, err := units.ParseByteSizeString(vol.config["size"])
	if err != nil {
		return err
	}

	newSize, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// The right disjunct just means that someone unset the size property in
	// the container's config. We obviously cannot resize to 0.
	if oldSize == newSize || newSize == 0 {
		return nil
	}

	if newSize < oldSize {
		err = shrinkFileSystem(fsType, RBDDevPath, vol, newSize)
		if err != nil {
			return err
		}

		_, err = shared.TryRunCommand(
			"rbd",
			"resize",
			"--allow-shrink",
			"--id", d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"--pool", d.config["ceph.osd.pool_name"],
			"--size", fmt.Sprintf("%dM", (newSize/1024/1024)),
			d.getRBDVolumeName(vol, "", false, false))
	} else {
		// Grow the block device
		_, err = shared.TryRunCommand(
			"rbd",
			"resize",
			"--id", d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"--pool", d.config["ceph.osd.pool_name"],
			"--size", fmt.Sprintf("%dM", (newSize/1024/1024)),
			d.getRBDVolumeName(vol, "", false, false))
		if err != nil {
			return err
		}

		// Grow the filesystem
		err = growFileSystem(fsType, RBDDevPath, vol)
	}
	if err != nil {
		return err
	}

	return nil
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *ceph) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() {
		devPath, _ := d.getRBDMappedDevPath(vol, true)
		return devPath, nil
	}

	return "", ErrNotImplemented
}

// MountVolume simulates mounting a volume.
func (d *ceph) MountVolume(vol Volume, op *operations.Operation) (bool, error) {
	mountPath := vol.MountPath()

	if vol.contentType == ContentTypeFS && !shared.IsMountPoint(mountPath) {
		RBDFilesystem := d.getRBDFilesystem(vol)
		ourMount := false

		err := vol.EnsureMountPath()
		if err != nil {
			return ourMount, err
		}

		RBDDevPath, ret := d.getRBDMappedDevPath(vol, true)

		if ret >= 0 {
			mountFlags, mountOptions := resolveMountOptions(d.getRBDMountOptions(vol))

			err = TryMount(RBDDevPath, mountPath, RBDFilesystem, mountFlags, mountOptions)
			ourMount = true
		}
		if err != nil || ret < 0 {
			return false, err
		}

		return ourMount, nil
	}

	// For VMs, mount the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		return d.MountVolume(fsVol, op)
	}

	return false, nil
}

// UnmountVolume simulates unmounting a volume.
func (d *ceph) UnmountVolume(vol Volume, op *operations.Operation) (bool, error) {
	// For VMs, also mount the filesystem dataset.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		_, err := d.UnmountVolume(fsVol, op)
		if err != nil {
			return false, err
		}
	}

	mountPath := vol.MountPath()

	if !shared.IsMountPoint(mountPath) {
		return false, nil
	}

	err := TryUnmount(mountPath, unix.MNT_DETACH)
	if err != nil {
		return false, err
	}

	// Attempt to unmap
	if vol.volType == VolumeTypeCustom {
		err = d.rbdUnmapVolume(vol, true)
		if err != nil {
			return true, err
		}
	}

	return true, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *ceph) RenameVolume(vol Volume, newName string, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	_, err := d.UnmountVolume(vol, op)
	if err != nil {
		return err
	}

	err = d.rbdUnmapVolume(vol, true)
	if err != nil {
		return nil
	}

	revert.Add(func() { d.rbdMapVolume(vol) })

	err = d.rbdRenameVolume(vol, newName)
	if err != nil {
		return err
	}

	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newName, nil, nil)

	revert.Add(func() { d.rbdRenameVolume(newVol, vol.name) })

	_, err = d.rbdMapVolume(newVol)
	if err != nil {
		return err
	}

	err = genericVFSRenameVolume(d, vol, newName, op)
	if err != nil {
		return nil
	}

	revert.Success()
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *ceph) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// If data is set, this request is coming from the clustering code.
	// In this case, we only need to unmap and rename the rbd image.
	if volSrcArgs.Data != nil {
		data, ok := volSrcArgs.Data.(string)
		if ok {
			err := d.rbdUnmapVolume(vol, true)
			if err != nil {
				return err
			}

			// Rename volume.
			if vol.name != data {
				err = d.rbdRenameVolume(vol, data)
				if err != nil {
					return err
				}
			}

			return nil
		}
	}

	// Handle simple rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC {
		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_RBD {
		return ErrNotSupported
	}

	if vol.IsSnapshot() {
		parentName, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(vol.name)
		sendName := fmt.Sprintf("%s/snapshots_%s_%s_start_clone", d.name, parentName, snapOnlyName)

		cloneVol := NewVolume(d, d.name, vol.volType, vol.contentType, vol.name, nil, nil)

		// Mounting the volume snapshot will create the clone "snapshots_<parent>_<snap>_start_clone".
		_, err := d.MountVolumeSnapshot(cloneVol, op)
		if err != nil {
			return err
		}
		defer d.UnmountVolumeSnapshot(cloneVol, op)

		// Setup progress tracking.
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		err = d.sendVolume(conn, sendName, "", wrapper)
		if err != nil {
			return err
		}

		return nil
	}

	lastSnap := ""

	if !volSrcArgs.FinalSync {
		for i, snapName := range volSrcArgs.Snapshots {
			snapshot, _ := vol.NewSnapshot(snapName)

			prev := ""

			if i > 0 {
				prev = fmt.Sprintf("snapshot_%s", volSrcArgs.Snapshots[i-1])
			}

			lastSnap = fmt.Sprintf("snapshot_%s", snapName)
			sendSnapName := d.getRBDVolumeName(vol, lastSnap, false, true)

			// Setup progress tracking.
			var wrapper *ioprogress.ProgressTracker

			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			err := d.sendVolume(conn, sendSnapName, prev, wrapper)
			if err != nil {
				return err
			}
		}
	}

	// Setup progress tracking.
	var wrapper *ioprogress.ProgressTracker
	if volSrcArgs.TrackProgress {
		wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
	}

	runningSnapName := fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())

	err := d.rbdCreateVolumeSnapshot(vol, runningSnapName)
	if err != nil {
		return err
	}
	defer d.rbdDeleteVolumeSnapshot(vol, runningSnapName)

	cur := d.getRBDVolumeName(vol, runningSnapName, false, true)

	err = d.sendVolume(conn, cur, lastSnap, wrapper)
	if err != nil {
		return err
	}

	return nil
}

// BackupVolume creates an exported version of a volume.
func (d *ceph) BackupVolume(vol Volume, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, targetPath, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *ceph) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	parentName, snapshotOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	snapshotName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)

	if shared.IsMountPoint(sourcePath) {
		// This is costly but we need to ensure that all cached data has
		// been committed to disk. If we don't then the rbd snapshot of
		// the underlying filesystem can be inconsistent or - worst case
		// - empty.
		unix.Sync()

		_, err := shared.TryRunCommand("fsfreeze", "--freeze", sourcePath)
		if err == nil {
			defer shared.TryRunCommand("fsfreeze", "--unfreeze", sourcePath)
		}
	}

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	err = snapVol.EnsureMountPath()
	if err != nil {
		return err
	}

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	err = d.rbdCreateVolumeSnapshot(parentVol, snapshotName)
	if err != nil {
		return err
	}

	revert.Add(func() { d.DeleteVolumeSnapshot(snapVol, op) })

	// For VM images, create a filesystem volume too.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolumeSnapshot(fsVol, op) })
	}

	revert.Success()
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *ceph) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	// Check if snapshot exists, and return if not.
	_, err := shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"info",
		d.getRBDVolumeName(snapVol, "", false, false))
	if err != nil {
		return nil
	}

	parentName, snapshotOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	snapshotName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	_, err = d.deleteVolumeSnapshot(parentVol, snapshotName)
	if err != nil {
		return errors.Wrap(err, "Failed to delete volume snapshot")
	}

	mountPath := snapVol.MountPath()

	if snapVol.contentType == ContentTypeFS && shared.PathExists(mountPath) {
		err = wipeDirectory(mountPath)
		if err != nil {
			return err
		}

		err = os.Remove(mountPath)
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Failed to remove '%s'", mountPath)
		}
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	// For VM images, delete the filesystem volume too.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountVolumeSnapshot simulates mounting a volume snapshot.
func (d *ceph) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	mountPath := snapVol.MountPath()

	if snapVol.contentType == ContentTypeFS && !shared.IsMountPoint(mountPath) {
		revert := revert.New()
		defer revert.Fail()

		parentName, snapshotOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
		prefixedSnapOnlyName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)

		parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

		// Protect snapshot to prevent data loss.
		err := d.rbdProtectVolumeSnapshot(parentVol, prefixedSnapOnlyName)
		if err != nil {
			return false, err
		}

		revert.Add(func() { d.rbdUnprotectVolumeSnapshot(parentVol, prefixedSnapOnlyName) })

		// Clone snapshot.
		cloneName := fmt.Sprintf("%s_%s_start_clone", parentName, snapshotOnlyName)
		cloneVol := NewVolume(d, d.name, VolumeType("snapshots"), ContentTypeFS, cloneName, nil, nil)

		err = d.rbdCreateClone(parentVol, prefixedSnapOnlyName, cloneVol)
		if err != nil {
			return false, err
		}

		revert.Add(func() { d.rbdDeleteVolume(cloneVol) })

		// Map volume
		rbdDevPath, err := d.rbdMapVolume(cloneVol)
		if err != nil {
			return false, err
		}

		revert.Add(func() { d.rbdUnmapVolume(cloneVol, true) })

		if shared.IsMountPoint(mountPath) {
			return false, nil
		}

		err = snapVol.EnsureMountPath()
		if err != nil {
			return false, err
		}

		RBDFilesystem := d.getRBDFilesystem(snapVol)
		mountFlags, mountOptions := resolveMountOptions(d.getRBDMountOptions(snapVol))
		if RBDFilesystem == "xfs" {
			idx := strings.Index(mountOptions, "nouuid")
			if idx < 0 {
				mountOptions += ",nouuid"
			}
		}

		err = TryMount(rbdDevPath, mountPath, RBDFilesystem, mountFlags, mountOptions)
		if err != nil {
			return false, err
		}

		revert.Success()

		return true, nil
	}

	// For VMs, mount the filesystem volume.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		return d.MountVolumeSnapshot(fsVol, op)
	}

	return false, nil
}

// UnmountVolume simulates unmounting a volume snapshot.
func (d *ceph) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	mountPath := snapVol.MountPath()

	if !shared.IsMountPoint(mountPath) {
		return false, nil
	}

	err := TryUnmount(mountPath, unix.MNT_DETACH)
	if err != nil {
		return false, err
	}

	parentName, snapshotOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	cloneName := fmt.Sprintf("%s_%s_start_clone", parentName, snapshotOnlyName)

	cloneVol := NewVolume(d, d.name, VolumeType("snapshots"), ContentTypeFS, cloneName, nil, nil)

	err = d.rbdUnmapVolume(cloneVol, true)
	if err != nil {
		return false, err
	}

	if !d.HasVolume(cloneVol) {
		return true, nil
	}

	// Delete the temporary RBD volume
	err = d.rbdDeleteVolume(cloneVol)
	if err != nil {
		return false, err
	}

	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		return d.UnmountVolumeSnapshot(fsVol, op)
	}

	return true, nil
}

// VolumeSnapshots returns a list of snapshots for the volume.
func (d *ceph) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	snapshots, err := d.rbdListVolumeSnapshots(vol)
	if err != nil {
		if err == db.ErrNoSuchObject {
			return nil, nil
		}

		return nil, err
	}

	var ret []string

	for _, snap := range snapshots {
		// Ignore zombie snapshots as these are only used internally and
		// not relevant for users.
		if strings.HasPrefix(snap, "zombie_") || strings.HasPrefix(snap, "migration-send-") {
			continue
		}

		ret = append(ret, strings.TrimPrefix(snap, "snapshot_"))
	}

	return ret, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *ceph) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	ourUmount, err := d.UnmountVolume(vol, op)
	if err != nil {
		return err
	}

	if ourUmount {
		defer d.MountVolume(vol, op)
	}

	_, err = shared.RunCommand(
		"rbd",
		"--id", d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"--pool", d.config["ceph.osd.pool_name"],
		"snap",
		"rollback",
		"--snap", fmt.Sprintf("snapshot_%s", snapshotName),
		d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		return err
	}

	snapVol, err := vol.NewSnapshot(snapshotName)
	if err != nil {
		return err
	}

	err = d.generateUUID(snapVol)
	if err != nil {
		return err
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *ceph) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	parentName, snapshotOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	oldSnapOnlyName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)
	newSnapOnlyName := fmt.Sprintf("snapshot_%s", newSnapshotName)

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	err := d.rbdRenameVolumeSnapshot(parentVol, oldSnapOnlyName, newSnapOnlyName)
	if err != nil {
		return err
	}

	revert.Add(func() { d.rbdRenameVolumeSnapshot(parentVol, newSnapOnlyName, oldSnapOnlyName) })

	if snapVol.contentType == ContentTypeFS {
		err = genericVFSRenameVolumeSnapshot(d, snapVol, newSnapshotName, op)
		if err != nil {
			return err
		}
	}

	// For VM images, create a filesystem volume too.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.RenameVolumeSnapshot(fsVol, newSnapshotName, op)
		if err != nil {
			return err
		}

		revert.Add(func() {
			newFsVol := NewVolume(d, d.name, snapVol.volType, ContentTypeFS, fmt.Sprintf("%s/%s", parentName, newSnapshotName), snapVol.config, snapVol.poolConfig)
			d.RenameVolumeSnapshot(newFsVol, snapVol.name, op)
		})
	}

	revert.Success()
	return nil
}
