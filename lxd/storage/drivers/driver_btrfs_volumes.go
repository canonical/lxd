package drivers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
)

// GetVolumeUsage returns the disk space used by the volume.
func (d *btrfs) GetVolumeUsage(vol Volume) (int64, error) {
	return d.getSubvolumeQGroupUsage(GetVolumeMountPath(d.name, vol.volType, vol.name))
}

// ValidateVolume validates the supplied volume config.
func (d *btrfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *btrfs) HasVolume(vol Volume) bool {
	return d.isSubvolume(GetVolumeMountPath(d.name, vol.volType, vol.name))
}

// GetVolumeDiskPath returns the location and file format of a disk volume.
func (d *btrfs) GetVolumeDiskPath(vol Volume) (string, error) {
	return filepath.Join(GetVolumeMountPath(d.name, vol.volType, vol.name), "root.img"), nil
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *btrfs) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	volPath := vol.MountPath()

	if vol.volType == VolumeTypeImage {
		// Since the image already exists we can return here.
		if shared.PathExists(volPath) && d.isSubvolume(volPath) {
			return nil
		}

		volPath = fmt.Sprintf("%s_tmp", vol.MountPath())

	}

	err := d.createSubvolume(volPath)
	if err != nil {
		return err
	}

	revertSubvolume := true
	defer func() {
		if revertSubvolume {
			d.deleteSubvolume(volPath)
		}
	}()

	// Extract specified size from pool or volume config.
	size := d.config["volume.size"]
	if vol.config["size"] != "" {
		size = vol.config["size"]
	}

	// Create sparse loopback file if volume is block.
	rootBlockPath := ""
	if vol.contentType == ContentTypeBlock {
		// We expect the filler to copy the VM image into this path.
		rootBlockPath, err = d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}
	}

	// Run the volume filler function if supplied.
	if filler != nil && filler.Fill != nil {
		err = filler.Fill(volPath, rootBlockPath)
		if err != nil {
			return err
		}
	}

	// If we are creating a block volume, resize it to the requested size or 10GB.
	// We expect the filler function to have converted the qcow2 image to raw into the rootBlockPath.
	if vol.contentType == ContentTypeBlock {
		blockSize := size
		if blockSize == "" {
			blockSize = "10GB"
		}

		blockSizeBytes, err := units.ParseByteSizeString(blockSize)
		if err != nil {
			return err
		}

		if shared.PathExists(rootBlockPath) {
			_, err = shared.RunCommand("qemu-img", "resize", "-f", "raw", rootBlockPath, fmt.Sprintf("%d", blockSizeBytes))
			if err != nil {
				return fmt.Errorf("Failed resizing disk image %s to size %s: %v", rootBlockPath, blockSize, err)
			}
		} else {
			// If rootBlockPath doesn't exist, then there has been no filler function
			// supplied to create it from another source. So instead create an empty
			// volume (use for PXE booting a VM).
			_, err = shared.RunCommand("qemu-img", "create", "-f", "raw", rootBlockPath, fmt.Sprintf("%d", blockSizeBytes))
			if err != nil {
				return fmt.Errorf("Failed creating disk image %s as size %s: %v", rootBlockPath, blockSize, err)
			}
		}
	} else {
		if vol.volType == VolumeTypeImage {
			// Create read-only snapshot of the temporary subvolume
			err = d.createSubvolumesSnapshot(volPath, vol.MountPath(), true, true, d.state.OS.RunningInUserNS)
			if err != nil {
				return err
			}

			defer func() {
				if revertSubvolume {
					d.deleteSubvolumes(vol.MountPath())
				}
			}()

			// Remove the temporary rw subvolume
			err = d.deleteSubvolumes(volPath)
			if err != nil {
				return err
			}
		} else {
			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}
		}
	}

	revertSubvolume = false
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *btrfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || vol.volType == VolumeTypeCustom {
		return d.migrateVolumeWithRsync(vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_BTRFS {
		// This is not needed if the migration is performed using btrfs
		// send/receive.
		if volSrcArgs.FinalSync {
			return nil
		}

		return d.migrateVolumeWithBtrfs(vol, conn, volSrcArgs, op)
	}

	return fmt.Errorf("Migration type not supported")
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *btrfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || vol.volType == VolumeTypeCustom || volTargetArgs.Refresh {
		return d.createVolumeFromMigrationWithRsync(vol, conn, volTargetArgs, preFiller, op)
	} else if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_BTRFS {
		return d.createVolumeFromMigrationWithBtrfs(vol, conn, volTargetArgs, preFiller, op)
	}

	return fmt.Errorf("Migration type not supported")
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *btrfs) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	err := os.MkdirAll(GetVolumeSnapshotDir(d.name, vol.volType, vol.name), 0711)
	if err != nil {
		return err
	}

	err = d.createSubvolumesSnapshot(srcVol.MountPath(), vol.MountPath(), false, true, d.state.OS.RunningInUserNS)
	if err != nil {
		return err
	}

	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	if !copySnapshots || srcVol.IsSnapshot() {
		return nil
	}

	snapshots, err := d.VolumeSnapshots(srcVol, op)
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		return nil
	}

	for _, snap := range snapshots {
		srcSnapshot := GetVolumeMountPath(d.name, srcVol.volType, GetSnapshotVolumeName(srcVol.name, snap))
		dstSnapshot := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snap))

		err = d.createSubvolumeSnapshot(srcSnapshot, dstSnapshot, true, d.state.OS.RunningInUserNS)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *btrfs) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS || srcVol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	bwlimit := d.config["rsync.bwlimit"]

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			snapVol, _ := vol.NewSnapshot(snapName)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if len(srcSnapshots) > 0 && !srcVol.IsSnapshot() {
			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := shared.InstanceGetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
					return err
				}, op)

				snapVol, err := vol.NewSnapshot(snapName)
				if err != nil {
					return err
				}

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(snapVol, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}
		// Copy source to destination (mounting each volume if needed).
		return srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
			return err
		}, op)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil // Don't revert.
	return nil
}

// VolumeSnapshots returns a list of snapshots for the volume.
func (d *btrfs) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return d.getSubvolumeSnapshots(GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
}

// UpdateVolume applies config changes to the volume.
func (d *btrfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if vol.volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	return d.SetVolumeQuota(vol, vol.config["size"], nil)
}

// RenameVolume renames a volume and its snapshots.
func (d *btrfs) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	srcVolumePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	dstVolumePath := GetVolumeMountPath(d.name, vol.volType, newVolName)

	err := os.Rename(srcVolumePath, dstVolumePath)
	if err != nil {
		return err
	}

	srcSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
	dstSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)

	if shared.PathExists(srcSnapshotDir) {
		err = os.Rename(srcSnapshotDir, dstSnapshotDir)
		if err != nil {
			return err
		}
	}

	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *btrfs) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	// Create a backup so we can revert.
	backupSubvolume := fmt.Sprintf("%s.tmp", vol.MountPath())

	err := os.Rename(vol.MountPath(), backupSubvolume)
	if err != nil {
		return err
	}

	undoSnapshot := true
	defer func() {
		if undoSnapshot {
			os.Rename(vol.MountPath(), backupSubvolume)
		}
	}()

	source := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapshotName))

	err = d.createSubvolumesSnapshot(source, vol.MountPath(), false, true, d.state.OS.RunningInUserNS)
	if err != nil {
		return err
	}

	undoSnapshot = false

	// Remove the backup subvolume
	return d.deleteSubvolumes(backupSubvolume)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *btrfs) DeleteVolume(vol Volume, op *operations.Operation) error {
	snapshots, err := d.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volPath := GetVolumeMountPath(d.name, vol.volType, vol.name)

	if !shared.PathExists(volPath) || !d.isSubvolume(volPath) {
		return nil
	}

	err = d.deleteSubvolume(volPath)
	if err != nil {
		return err
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	err = deleteParentSnapshotDirIfEmpty(d.name, vol.volType, vol.name)
	if err != nil {
		return err
	}

	return nil
}

// MountVolume simulates mounting a volume. As dir driver doesn't have volumes to mount it returns
// false indicating that there is no need to issue an unmount.
func (d *btrfs) MountVolume(vol Volume, op *operations.Operation) (bool, error) {
	_, err := d.Mount()
	if err != nil {
		return false, err
	}

	return true, nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *btrfs) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	_, err := d.Mount()
	if err != nil {
		return false, err
	}

	return true, nil
}

// UnmountVolume simulates unmounting a volume. As dir driver doesn't have volumes to unmount it
// returns false indicating the volume was already unmounted.
func (d *btrfs) UnmountVolume(vol Volume, op *operations.Operation) (bool, error) {
	return false, nil
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *btrfs) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	return false, nil
}

// SetVolumeQuota sets the quota on the volume.
func (d *btrfs) SetVolumeQuota(vol Volume, size string, op *operations.Operation) error {
	subvol := GetVolumeMountPath(d.name, vol.volType, vol.name)

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	qgroup, err := d.getSubvolumeQGroup(subvol)
	if err != nil && !d.state.OS.RunningInUserNS {
		var output string

		if err == errBtrfsNoQuota {
			// Enable quotas
			poolMntPoint := GetPoolMountPath(d.name)

			_, err = shared.RunCommand("btrfs", "quota", "enable", poolMntPoint)
			if err != nil {
				return fmt.Errorf("Failed to enable quotas on BTRFS pool: %v", err)
			}

			// Retry
			qgroup, err = d.getSubvolumeQGroup(subvol)
		}

		if err == errBtrfsNoQGroup {
			// Find the volume ID
			_, err = shared.RunCommand("btrfs", "subvolume", "show", subvol)
			if err != nil {
				return fmt.Errorf("Failed to get subvol information: %v", err)
			}

			id := ""
			for _, line := range strings.Split(output, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Subvolume ID:") {
					fields := strings.Split(line, ":")
					id = strings.TrimSpace(fields[len(fields)-1])
				}
			}

			if id == "" {
				return fmt.Errorf("Failed to find subvolume id")
			}

			// Create qgroup
			_, err = shared.RunCommand("btrfs", "qgroup", "create", fmt.Sprintf("0/%s", id), subvol)
			if err != nil {
				return fmt.Errorf("Failed to create missing qgroup: %v", err)
			}

			// Retry
			qgroup, err = d.getSubvolumeQGroup(subvol)
		}

		if err != nil {
			return err
		}
	}

	// Attempt to make the subvolume writable
	shared.RunCommand("btrfs", "property", "set", subvol, "ro", "false")
	if sizeBytes > 0 {
		_, err := shared.RunCommand(
			"btrfs",
			"qgroup",
			"limit",
			"-e", fmt.Sprintf("%d", sizeBytes),
			subvol)

		if err != nil {
			return fmt.Errorf("Failed to set btrfs quota: %v", err)
		}
	} else if qgroup != "" {
		_, err := shared.RunCommand(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol)

		if err != nil {
			return fmt.Errorf("Failed to set btrfs quota: %v", err)
		}
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *btrfs) CreateVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vol.name)
	sourcePath := GetVolumeMountPath(d.name, vol.volType, parentName)
	targetPath := GetVolumeMountPath(d.name, vol.volType, vol.name)

	err := os.MkdirAll(GetVolumeSnapshotDir(d.name, vol.volType, parentName), 0700)
	if err != nil {
		return err
	}

	return d.createSubvolumesSnapshot(sourcePath, targetPath, true, true, d.state.OS.RunningInUserNS)
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *btrfs) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return d.deleteSubvolumes(GetVolumeMountPath(d.name, snapVol.volType, snapVol.name))
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *btrfs) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	oldFullSnapshotName := snapVol.name
	newFullSnapshotName := GetSnapshotVolumeName(parentName, newSnapshotName)

	oldPath := GetVolumeMountPath(d.name, snapVol.volType, oldFullSnapshotName)
	newPath := GetVolumeMountPath(d.name, snapVol.volType, newFullSnapshotName)

	return os.Rename(oldPath, newPath)
}

func (d *btrfs) BackupVolume(vol Volume, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	if optimized {
		return d.backupVolumeWithBtrfs(vol, targetPath, snapshots, op)
	}

	return d.backupVolumeWithRsync(vol, targetPath, snapshots, op)
}

func (d *btrfs) CreateVolumeFromBackup(vol Volume, snapshots []string, srcData io.ReadSeeker, optimized bool, op *operations.Operation) (func(vol Volume) error, func(), error) {
	if optimized {
		return d.restoreBackupVolumeOptimized(vol, snapshots, srcData, op)
	}

	return d.restoreBackupVolume(vol, snapshots, srcData, op)
}
