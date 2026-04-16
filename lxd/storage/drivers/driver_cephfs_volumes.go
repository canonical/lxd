package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
)

// CreateVolume creates a new storage volume on disk.
func (d *cephfs) CreateVolume(vol Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	if vol.volType != VolumeTypeCustom {
		return ErrNotSupported
	}

	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Setup for revert.
	revertPath := true
	defer func() {
		if revertPath {
			_ = os.RemoveAll(volPath)
		}
	}()

	// Apply the volume quota if specified.
	err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, progressReporter)
	if err != nil {
		return err
	}

	// Fill the volume.
	err = d.runFiller(vol, "", filler, false)
	if err != nil {
		return err
	}

	revertPath = false
	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *cephfs) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state, vol, srcBackup.Snapshots, srcData, progressReporter)
}

// CreateVolumeFromImage creates a new volume from an image, unpacking it directly.
func (d *cephfs) CreateVolumeFromImage(vol Volume, imgVol *Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	return d.CreateVolume(vol, filler, progressReporter)
}

// CreateVolumeFromCopy copies an existing storage volume (with or without snapshots) into a new volume.
func (d *cephfs) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error {
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
			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)

			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)
			_ = d.DeleteVolumeSnapshot(snapVol, progressReporter)
		}

		_ = os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, progressReporter ioprogress.ProgressReporter) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if len(vol.Snapshots) > 0 && !srcVol.IsSnapshot() {
			// Get the list of snapshots from the source.
			srcSnapshots, err := srcVol.Volume.Snapshots(progressReporter)
			if err != nil {
				return err
			}

			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := api.GetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, progressReporter ioprogress.ProgressReporter) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
					return err
				}, progressReporter)

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(srcSnapshot, progressReporter)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}

		// Apply the volume quota if specified.
		err = d.SetVolumeQuota(vol.Volume, vol.ConfigSize(), false, progressReporter)
		if err != nil {
			return err
		}

		// Copy source to destination (mounting each volume if needed).
		err = srcVol.MountTask(func(srcMountPath string, progressReporter ioprogress.ProgressReporter) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
			return err
		}, progressReporter)
		if err != nil {
			return err
		}

		// Run EnsureMountPath after mounting and copying to ensure the mounted directory has the
		// correct permissions set.
		return vol.EnsureMountPath()
	}, progressReporter)
	if err != nil {
		return err
	}

	revertSnaps = nil // Do not revert.
	return nil
}

// CreateVolumeFromMigration creates a new volume (with or without snapshots) from a migration data stream.
func (d *cephfs) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

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
			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

			_ = d.DeleteVolumeSnapshot(snapVol, progressReporter)
		}

		_ = os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, progressReporter ioprogress.ProgressReporter) error {
		path := shared.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot.
			var wrapper ioprogress.ReaderWrapper
			if volTargetArgs.TrackProgress {
				wrapper = ioprogress.NewProgressReaderWrapper(ioprogress.WithDescriptiveProgressReporter("fs", snapName, progressReporter))
			}

			err = rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
			if err != nil {
				return err
			}

			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

			// Create the snapshot itself.
			err = d.CreateVolumeSnapshot(snapVol, progressReporter)
			if err != nil {
				return err
			}

			// Setup the revert.
			revertSnaps = append(revertSnaps, snapName)
		}

		if vol.contentType == ContentTypeFS {
			// Apply the size limit.
			err = d.SetVolumeQuota(vol.Volume, vol.ConfigSize(), false, progressReporter)
			if err != nil {
				return err
			}
		}

		// Receive the main volume from sender.
		var wrapper ioprogress.ReaderWrapper
		if volTargetArgs.TrackProgress {
			wrapper = ioprogress.NewProgressReaderWrapper(ioprogress.WithDescriptiveProgressReporter("fs", vol.name, progressReporter))
		}

		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}, progressReporter)
	if err != nil {
		return err
	}

	revertSnaps = nil
	return nil
}

// DeleteVolume destroys the on-disk state of a volume.
func (d *cephfs) DeleteVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error {
	snapshots, err := d.VolumeSnapshots(vol)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return errors.New("Cannot remove a volume that has snapshots")
	}

	volPath := GetVolumeMountPath(d.name, vol.volType, vol.name)

	// Remove the volume from the storage device.
	err = os.RemoveAll(volPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed deleting %q: %w", volPath, err)
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	err = os.RemoveAll(snapshotDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed deleting %q: %w", snapshotDir, err)
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *cephfs) HasVolume(vol Volume) (bool, error) {
	return genericVFSHasVolume(vol)
}

// ValidateVolume validates the supplied volume config. Optionally removes invalid keys from the volume's config.
func (d *cephfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// UpdateVolume applies the driver specific changes of a volume configuration change.
func (d *cephfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space usage of a volume.
func (d *cephfs) GetVolumeUsage(vol Volume) (int64, error) {
	// Snapshot usage not supported for CephFS.
	if vol.IsSnapshot() {
		return -1, ErrNotSupported
	}

	out, err := shared.RunCommand(context.TODO(), "getfattr", "-n", "ceph.quota.max_bytes", "--only-values", GetVolumeMountPath(d.name, vol.volType, vol.name))
	if err != nil {
		return -1, err
	}

	size, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return -1, err
	}

	return size, nil
}

// SetVolumeQuota applies a size limit on volume.
func (d *cephfs) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, progressReporter ioprogress.ProgressReporter) error {
	// If size not specified in volume config, then use pool's default volume.size setting.
	if size == "" || size == "0" {
		size = d.config["volume.size"]
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand(context.TODO(), "setfattr", "-n", "ceph.quota.max_bytes", "-v", strconv.FormatInt(sizeBytes, 10), GetVolumeMountPath(d.name, vol.volType, vol.name))
	return err
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *cephfs) GetVolumeDiskPath(vol Volume) (string, error) {
	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *cephfs) ListVolumes() ([]Volume, error) {
	return genericVFSListVolumes(d)
}

// MountVolume sets up the volume for use.
func (d *cephfs) MountVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	return nil
}

// UnmountVolume clears any runtime state for the volume.
// As driver doesn't have volumes to unmount it returns false indicating the volume was already unmounted.
func (d *cephfs) UnmountVolume(vol Volume, keepBlockDev bool, progressReporter ioprogress.ProgressReporter) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	refCount := vol.MountRefCountDecrement()
	if refCount > 0 {
		d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
		return false, ErrInUse
	}

	return false, nil
}

// RenameVolume renames the volume and all related filesystem entries.
func (d *cephfs) RenameVolume(vol Volume, newVolName string, progressReporter ioprogress.ProgressReporter) error {
	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, vol.volType, newVolName)
	if err != nil {
		return err
	}

	type volRevert struct {
		oldPath   string
		newPath   string
		isSymlink bool
	}

	// Create slice to record paths renamed if revert needed later.
	revertPaths := []volRevert{}
	defer func() {
		// Remove any paths rename if we are reverting.
		for _, vol := range revertPaths {
			if vol.isSymlink {
				_ = os.Symlink(vol.oldPath, vol.newPath)
			} else {
				_ = os.Rename(vol.newPath, vol.oldPath)
			}
		}

		// Remove the new snapshot directory if we are reverting.
		if len(revertPaths) > 0 {
			snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)
			_ = os.RemoveAll(snapshotDir)
		}
	}()

	// Rename the snapshot directory first.
	srcSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
	targetSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)

	err = os.Rename(srcSnapshotDir, targetSnapshotDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed renaming %q to %q: %w", srcSnapshotDir, targetSnapshotDir, err)
	}

	if err == nil {
		revertPaths = append(revertPaths, volRevert{
			oldPath: srcSnapshotDir,
			newPath: targetSnapshotDir,
		})
	}

	// Rename any snapshots of the volume too.
	snapshots, err := vol.Snapshots(progressReporter)
	if err != nil {
		return err
	}

	sourcePath := GetVolumeMountPath(d.name, vol.volType, newVolName)
	targetPath := GetVolumeMountPath(d.name, vol.volType, newVolName)

	for _, snapshot := range snapshots {
		// Figure out the snapshot paths.
		_, snapName, _ := api.GetParentAndSnapshotName(snapshot.name)
		oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapName)
		newCephSnapPath := filepath.Join(targetPath, ".snap", snapName)
		oldPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapName))
		newPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(newVolName, snapName))

		// Update the symlink.
		err = os.Symlink(newCephSnapPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed symlinking %q to %q: %w", newCephSnapPath, newPath, err)
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath:   oldPath,
			newPath:   oldCephSnapPath,
			isSymlink: true,
		})
	}

	oldPath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	newPath := GetVolumeMountPath(d.name, vol.volType, newVolName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("Failed renaming %q to %q: %w", oldPath, newPath, err)
	}

	revertPaths = append(revertPaths, volRevert{
		oldPath: oldPath,
		newPath: newPath,
	})

	revertPaths = nil
	return nil
}

// MigrateVolume streams the volume (with or without snapshots).
func (d *cephfs) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error {
	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, progressReporter)
}

// BackupVolume creates an exported version of a volume.
func (d *cephfs) BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, progressReporter ioprogress.ProgressReporter) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, progressReporter)
}

// CreateVolumeSnapshot creates a new snapshot.
func (d *cephfs) CreateVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Create the snapshot.
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err := os.Mkdir(cephSnapPath, 0711)
	if err != nil {
		return fmt.Errorf("Failed creating directory %q: %w", cephSnapPath, err)
	}

	// Create the parent directory.
	err = createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	// Create the symlink.
	targetPath := snapVol.MountPath()
	err = os.Symlink(cephSnapPath, targetPath)
	if err != nil {
		return fmt.Errorf("Failed symlinking %q to %q: %w", cephSnapPath, targetPath, err)
	}

	return nil
}

// DeleteVolumeSnapshot deletes a snapshot.
func (d *cephfs) DeleteVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Delete the snapshot itself.
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err := os.Remove(cephSnapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed removing %q: %w", cephSnapPath, err)
	}

	// Remove the symlink.
	snapPath := snapVol.MountPath()
	err = os.Remove(snapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed removing %q: %w", snapPath, err)
	}

	return nil
}

// MountVolumeSnapshot makes the snapshot available for use.
func (d *cephfs) MountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	return nil
}

// UnmountVolumeSnapshot clears any runtime state for the snapshot.
func (d *cephfs) UnmountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) (bool, error) {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	refCount := snapVol.MountRefCountDecrement()
	if refCount > 0 {
		d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
		return false, ErrInUse
	}

	return false, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *cephfs) VolumeSnapshots(vol Volume) ([]string, error) {
	return genericVFSVolumeSnapshots(d, vol)
}

// RestoreVolume resets a volume to its snapshotted state.
func (d *cephfs) RestoreVolume(vol Volume, snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	sourcePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.name)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapshotName)

	// Restore using rsync.
	bwlimit := d.config["rsync.bwlimit"]
	output, err := rsync.LocalCopy(cephSnapPath, vol.MountPath(), bwlimit, false)
	if err != nil {
		return fmt.Errorf("Failed rsyncing volume: %s: %w", string(output), err)
	}

	return nil
}

// RenameVolumeSnapshot renames a snapshot.
func (d *cephfs) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, progressReporter ioprogress.ProgressReporter) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapName)
	newCephSnapPath := filepath.Join(sourcePath, ".snap", newSnapshotName)

	err := os.Rename(oldCephSnapPath, newCephSnapPath)
	if err != nil {
		return fmt.Errorf("Failed renaming %q to %q: %w", oldCephSnapPath, newCephSnapPath, err)
	}

	// Re-generate the snapshot symlink.
	oldPath := snapVol.MountPath()
	err = os.Remove(oldPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed removing %q: %w", oldPath, err)
	}

	newPath := GetVolumeMountPath(d.name, snapVol.volType, GetSnapshotVolumeName(parentName, newSnapshotName))
	err = os.Symlink(newCephSnapPath, newPath)
	if err != nil {
		return fmt.Errorf("Failed symlinking %q to %q: %w", newCephSnapPath, newPath, err)
	}

	return nil
}
