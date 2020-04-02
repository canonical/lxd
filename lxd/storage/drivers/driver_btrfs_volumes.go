package drivers

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/instancewriter"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/units"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *btrfs) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	volPath := vol.MountPath()

	// Create the volume itself.
	_, err := shared.RunCommand("btrfs", "subvolume", "create", volPath)
	if err != nil {
		return err
	}

	// Setup revert.
	revertPath := true
	defer func() {
		if revertPath {
			d.deleteSubvolume(volPath, false)
		}
	}()

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

	// If we are creating a block volume, resize it to the requested size or the default.
	// We expect the filler function to have converted the qcow2 image to raw into the rootBlockPath.
	if vol.contentType == ContentTypeBlock {
		err := ensureVolumeBlockFile(rootBlockPath, vol.ExpandedConfig("size"))
		if err != nil {
			return err
		}

		// Move the GPT alt header to end of disk if needed.
		if vol.IsVMBlock() {
			err = d.moveGPTAltHeader(rootBlockPath)
			if err != nil {
				return err
			}
		}
	} else if vol.contentType == ContentTypeFS {
		// Set initial quota for filesystem volumes.
		err := d.SetVolumeQuota(vol, vol.ExpandedConfig("size"), op)
		if err != nil {
			return err
		}
	}

	// Tweak any permissions that need tweaking.
	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Attempt to mark image read-only.
	if vol.volType == VolumeTypeImage {
		_, err = shared.RunCommand("btrfs", "property", "set", volPath, "ro", "true")
		if err != nil && !d.state.OS.RunningInUserNS {
			return err
		}
	}

	revertPath = false
	return nil
}

// CreateVolumeFromBackup restores a backup tarball onto the storage device.
func (d *btrfs) CreateVolumeFromBackup(vol Volume, snapshots []string, srcData io.ReadSeeker, optimized bool, op *operations.Operation) (func(vol Volume) error, func(), error) {
	// Handle the non-optimized tarballs through the generic unpacker.
	if !optimized {
		return genericVFSBackupUnpack(d, vol, snapshots, srcData, op)
	}

	if d.HasVolume(vol) {
		return nil, nil, fmt.Errorf("Cannot restore volume, already exists on target")
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a revert function that will be used both to revert if an error occurs inside this
	// function but also return it for use from the calling functions if no error internally.
	revertHook := func() {
		for _, snapName := range snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		// And lastly the main volume.
		d.DeleteVolume(vol, op)
	}
	// Only execute the revert function if we have had an error internally.
	revert.Add(revertHook)

	// Define function to unpack a volume from a backup tarball file.
	unpackVolume := func(r io.ReadSeeker, unpacker []string, srcFile string, mountPath string) error {
		d.Logger().Debug("Unpacking optimized volume", log.Ctx{"source": srcFile, "target": mountPath})
		tr, cancelFunc, err := shared.CompressedTarReader(context.Background(), r, unpacker)
		if err != nil {
			return err
		}
		defer cancelFunc()

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break // End of archive
			}
			if err != nil {
				return err
			}

			if hdr.Name == srcFile {
				// Extract the backup.
				err = shared.RunCommandWithFds(tr, nil, "btrfs", "receive", "-e", mountPath)
				if err != nil {
					return err
				}

				cancelFunc()
				return nil
			}
		}

		return fmt.Errorf("Could not find %q", srcFile)
	}

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	_, _, unpacker, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	// Restore backups from oldest to newest.
	snapshotsDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
	for _, snapName := range snapshots {
		prefix := "snapshots"
		fileName := fmt.Sprintf("%s.bin", snapName)
		if vol.volType == VolumeTypeVM {
			prefix = "virtual-machine-snapshots"
			if vol.contentType == ContentTypeFS {
				fileName = fmt.Sprintf("%s-config.bin", snapName)
			}
		}

		srcFile := fmt.Sprintf("backup/%s/%s", prefix, fileName)
		err = unpackVolume(srcData, unpacker, srcFile, snapshotsDir)
		if err != nil {
			return nil, nil, err
		}
	}

	// Create a temporary directory to unpack the backup into.
	unpackDir, err := ioutil.TempDir(GetVolumeMountPath(d.name, vol.volType, ""), "backup.")
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to create temporary directory under '%s'", GetVolumeMountPath(d.name, vol.volType, ""))
	}
	defer os.RemoveAll(unpackDir)

	err = os.Chmod(unpackDir, 0100)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to chmod '%s'", unpackDir)
	}

	// Extract main volume.
	fileName := "container.bin"
	if vol.volType == VolumeTypeVM {
		if vol.contentType == ContentTypeFS {
			fileName = "virtual-machine-config.bin"
		} else {
			fileName = "virtual-machine.bin"
		}
	}

	err = unpackVolume(srcData, unpacker, fmt.Sprintf("backup/%s", fileName), unpackDir)
	if err != nil {
		return nil, nil, err
	}
	defer d.deleteSubvolume(filepath.Join(unpackDir, ".backup"), true)

	// Re-create the writable subvolume.
	err = d.snapshotSubvolume(filepath.Join(unpackDir, ".backup"), vol.MountPath(), false, false)
	if err != nil {
		return nil, nil, err
	}

	revert.Success()
	return nil, revertHook, nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *btrfs) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	// Recursively copy the main volume.
	err := d.snapshotSubvolume(srcVol.MountPath(), vol.MountPath(), false, true)
	if err != nil {
		return err
	}

	// Set quota for volume.
	err = d.SetVolumeQuota(vol, vol.ExpandedConfig("size"), op)
	if err != nil {
		return err
	}

	// Fixup permissions.
	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// If we're not copying any snapshots, we're done here.
	if !copySnapshots || srcVol.IsSnapshot() {
		return nil
	}

	// Get the list of snapshots.
	snapshots, err := d.VolumeSnapshots(srcVol, op)
	if err != nil {
		return err
	}

	// If no snapshots, we're done here.
	if len(snapshots) == 0 {
		return nil
	}

	// Create the parent directory.
	err = createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
	if err != nil {
		return err
	}

	// Copy the snapshots.
	for _, snapName := range snapshots {
		srcSnapshot := GetVolumeMountPath(d.name, srcVol.volType, GetSnapshotVolumeName(srcVol.name, snapName))
		dstSnapshot := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapName))

		err = d.snapshotSubvolume(srcSnapshot, dstSnapshot, true, false)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *btrfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// Handle simple rsync and block_and_rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volTargetArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		return genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_BTRFS {
		return ErrNotSupported
	}

	// Handle btrfs send/receive migration.
	if len(volTargetArgs.Snapshots) > 0 {
		snapshotsDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

		// Create the parent directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Transfer the snapshots.
		for _, snapName := range volTargetArgs.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			wrapper := migration.ProgressWriter(op, "fs_progress", fullSnapshotName)

			err = d.receiveSubvolume(snapshotsDir, snapshotsDir, conn, wrapper)
			if err != nil {
				return err
			}
		}
	}

	// Get instances directory (e.g. /var/lib/lxd/storage-pools/btrfs/containers).
	instancesPath := GetVolumeMountPath(d.name, vol.volType, "")

	// Create a temporary directory which will act as the parent directory of the received ro snapshot.
	tmpVolumesMountPoint, err := ioutil.TempDir(instancesPath, "migration.")
	if err != nil {
		return errors.Wrapf(err, "Failed to create temporary directory under '%s'", instancesPath)
	}
	defer os.RemoveAll(tmpVolumesMountPoint)

	err = os.Chmod(tmpVolumesMountPoint, 0100)
	if err != nil {
		return errors.Wrapf(err, "Failed to chmod '%s'", tmpVolumesMountPoint)
	}

	wrapper := migration.ProgressWriter(op, "fs_progress", vol.name)
	err = d.receiveSubvolume(tmpVolumesMountPoint, vol.MountPath(), conn, wrapper)
	if err != nil {
		return err
	}

	return nil
}

// RefreshVolume provides same-pool volume and specific snapshots syncing functionality.
func (d *btrfs) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error {
	return genericVFSCopyVolume(d, nil, vol, srcVol, srcSnapshots, true, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *btrfs) DeleteVolume(vol Volume, op *operations.Operation) error {
	// Check that we don't have snapshots.
	snapshots, err := d.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	// If the volume doesn't exist, then nothing more to do.
	volPath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	if !shared.PathExists(volPath) {
		return nil
	}

	// Delete the volume (and any subvolumes).
	err = d.deleteSubvolume(volPath, true)
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

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *btrfs) HasVolume(vol Volume) bool {
	return genericVFSHasVolume(vol)
}

// ValidateVolume validates the supplied volume config.
func (d *btrfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *btrfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	if vol.volType != VolumeTypeCustom {
		return ErrNotSupported
	}

	if _, changed := changedConfig["size"]; changed {
		err := d.SetVolumeQuota(vol, changedConfig["size"], nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *btrfs) GetVolumeUsage(vol Volume) (int64, error) {
	// Attempt to get the qgroup information.
	_, usage, err := d.getQGroup(vol.MountPath())
	if err != nil {
		if err == errBtrfsNoQuota {
			return 0, nil
		}

		return -1, err
	}

	return usage, nil
}

// SetVolumeQuota sets the quota on the volume.
func (d *btrfs) SetVolumeQuota(vol Volume, size string, op *operations.Operation) error {
	// For VM block files, resize the file if needed.
	if vol.contentType == ContentTypeBlock {
		rootBlockPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		// If size not specified in volume config, then use pool's default block size.
		if size == "" || size == "0" {
			size = defaultBlockSize
		}

		resized, err := genericVFSResizeBlockFile(rootBlockPath, size)
		if err != nil {
			return err
		}

		// Move the GPT alt header to end of disk if needed and resize has taken place.
		if vol.IsVMBlock() && resized {
			err = d.moveGPTAltHeader(rootBlockPath)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// For non-VM block volumes, set filesystem quota.
	volPath := vol.MountPath()

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Try to locate an existing quota group.
	qgroup, _, err := d.getQGroup(volPath)
	if err != nil && !d.state.OS.RunningInUserNS {
		// If quotas are disabled, attempt to enable them.
		if err == errBtrfsNoQuota {
			if sizeBytes <= 0 {
				// Nothing to do if the quota is being removed and we don't currently have quota.
				return nil
			}

			path := GetPoolMountPath(d.name)

			_, err = shared.RunCommand("btrfs", "quota", "enable", path)
			if err != nil {
				return err
			}

			// Try again.
			qgroup, _, err = d.getQGroup(volPath)
		}

		// If there's no qgroup, attempt to create one.
		if err == errBtrfsNoQGroup {
			// Find the volume ID.
			var output string
			output, err = shared.RunCommand("btrfs", "subvolume", "show", volPath)
			if err != nil {
				return errors.Wrap(err, "Failed to get subvol information")
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
				return fmt.Errorf("Failed to find subvolume id for %s", volPath)
			}

			// Create a qgroup.
			_, err = shared.RunCommand("btrfs", "qgroup", "create", fmt.Sprintf("0/%s", id), volPath)
			if err != nil {
				return err
			}

			// Try to get the qgroup again.
			qgroup, _, err = d.getQGroup(volPath)
		}

		if err != nil {
			return err
		}
	}

	// Modify the limit.
	if sizeBytes > 0 {
		// Apply the limit.
		_, err := shared.RunCommand("btrfs", "qgroup", "limit", fmt.Sprintf("%d", sizeBytes), volPath)
		if err != nil {
			return err
		}
	} else if qgroup != "" {
		// Remove the limit.
		_, err := shared.RunCommand("btrfs", "qgroup", "limit", "none", qgroup, volPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeDiskPath returns the location and file format of a disk volume.
func (d *btrfs) GetVolumeDiskPath(vol Volume) (string, error) {
	return genericVFSGetVolumeDiskPath(vol)
}

// MountVolume simulates mounting a volume.
func (d *btrfs) MountVolume(vol Volume, op *operations.Operation) (bool, error) {
	return true, nil
}

// UnmountVolume simulates unmounting a volume.
func (d *btrfs) UnmountVolume(vol Volume, op *operations.Operation) (bool, error) {
	return false, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *btrfs) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	return genericVFSRenameVolume(d, vol, newVolName, op)
}

// MigrateVolume sends a volume for migration.
func (d *btrfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	// Handle simple rsync and block_and_rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volSrcArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_BTRFS {
		return ErrNotSupported
	}

	// Handle btrfs send/receive migration.
	if volSrcArgs.FinalSync {
		// This is not needed if the migration is performed using btrfs send/receive.
		return nil
	}

	// Transfer the snapshots first.
	for i, snapName := range volSrcArgs.Snapshots {
		snapshot, _ := vol.NewSnapshot(snapName)

		// Locate the parent snapshot.
		parentSnapshotPath := ""
		if i > 0 {
			parentSnapshotPath = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSrcArgs.Snapshots[i-1]))
		}

		// Setup progress tracking.
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
		}

		// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
		err := d.sendSubvolume(snapshot.MountPath(), parentSnapshotPath, conn, wrapper)
		if err != nil {
			return err
		}
	}

	// Get instances directory (e.g. /var/lib/lxd/storage-pools/btrfs/containers).
	instancesPath := GetVolumeMountPath(d.name, vol.volType, "")

	// Create a temporary directory which will act as the parent directory of the read-only snapshot.
	tmpVolumesMountPoint, err := ioutil.TempDir(instancesPath, "migration.")
	if err != nil {
		return errors.Wrapf(err, "Failed to create temporary directory under '%s'", instancesPath)
	}
	defer os.RemoveAll(tmpVolumesMountPoint)

	err = os.Chmod(tmpVolumesMountPoint, 0100)
	if err != nil {
		return errors.Wrapf(err, "Failed to chmod '%s'", tmpVolumesMountPoint)
	}

	// Make read-only snapshot of the subvolume as writable subvolumes cannot be sent.
	migrationSendSnapshot := filepath.Join(tmpVolumesMountPoint, ".migration-send")
	err = d.snapshotSubvolume(vol.MountPath(), migrationSendSnapshot, true, false)
	if err != nil {
		return err
	}
	defer d.deleteSubvolume(migrationSendSnapshot, true)

	// Setup progress tracking.
	var wrapper *ioprogress.ProgressTracker
	if volSrcArgs.TrackProgress {
		wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
	}

	// Compare to latest snapshot.
	btrfsParent := ""
	if len(volSrcArgs.Snapshots) > 0 {
		btrfsParent = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSrcArgs.Snapshots[len(volSrcArgs.Snapshots)-1]))
	}

	// Send the volume itself.
	err = d.sendSubvolume(migrationSendSnapshot, btrfsParent, conn, wrapper)
	if err != nil {
		return err
	}

	return nil
}

// BackupVolume copies a volume (and optionally its snapshots) to a specified target path.
// This driver does not support optimized backups.
func (d *btrfs) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	// Handle the non-optimized tarballs through the generic packer.
	if !optimized {
		// Because the generic backup method will not take a consistent backup if files are being modified
		// as they are copied to the tarball, as BTRFS allows us to take a quick snapshot without impacting
		// the parent volume we do so here to ensure the backup taken is consistent.
		if vol.contentType == ContentTypeFS {
			sourcePath := vol.MountPath()
			poolPath := GetPoolMountPath(d.name)
			tmpDir, err := ioutil.TempDir(poolPath, "backup.")
			if err != nil {
				return errors.Wrapf(err, "Failed to create temporary directory under %q", poolPath)
			}
			defer os.RemoveAll(tmpDir)

			err = os.Chmod(tmpDir, 0100)
			if err != nil {
				return errors.Wrapf(err, "Failed to chmod %q", tmpDir)
			}

			// Override volume's mount path with location of snapshot so genericVFSBackupVolume reads
			// from there instead of main volume.
			vol.customMountPath = filepath.Join(tmpDir, vol.name)

			// Create the read-only snapshot.
			mountPath := vol.MountPath()
			err = d.snapshotSubvolume(sourcePath, mountPath, true, true)
			if err != nil {
				return err
			}
			d.logger.Debug("Created read-only backup snapshot", log.Ctx{"sourcePath": sourcePath, "path": mountPath})
			defer d.deleteSubvolume(mountPath, true)
		}

		return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
	}

	// Handle the optimized tarballs.
	sendToFile := func(path string, parent string, fileName string) error {
		// Prepare btrfs send arguments.
		args := []string{"send"}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		args = append(args, path)

		// Create temporary file to store output of btrfs send.
		backupsPath := shared.VarPath("backups")
		tmpFile, err := ioutil.TempFile(backupsPath, "lxd_backup_btrfs")
		if err != nil {
			return errors.Wrapf(err, "Failed to open temporary file for BTRFS backup")
		}
		defer tmpFile.Close()
		defer os.Remove(tmpFile.Name())

		// Write the subvolume to the file.
		d.logger.Debug("Generating optimized volume file", log.Ctx{"sourcePath": path, "file": tmpFile.Name(), "name": fileName})
		err = shared.RunCommandWithFds(nil, tmpFile, "btrfs", args...)
		if err != nil {
			return err
		}

		// Get info (importantly size) of the generated file for tarball header.
		tmpFileInfo, err := os.Lstat(tmpFile.Name())
		if err != nil {
			return err
		}

		err = tarWriter.WriteFile(fileName, tmpFile.Name(), tmpFileInfo, false)
		if err != nil {
			return err
		}

		return tmpFile.Close()
	}

	// Handle snapshots.
	finalParent := ""
	if snapshots {
		// Retrieve the snapshots.
		volSnapshots, err := d.VolumeSnapshots(vol, op)
		if err != nil {
			return err
		}

		for i, snap := range volSnapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snap)

			// Figure out parent and current subvolumes.
			parent := ""
			if i > 0 {
				parent = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSnapshots[i-1]))
			}

			cur := GetVolumeMountPath(d.name, vol.volType, fullSnapshotName)

			// Make a binary btrfs backup.
			prefix := "snapshots"
			fileName := fmt.Sprintf("%s.bin", snap)
			if vol.volType == VolumeTypeVM {
				prefix = "virtual-machine-snapshots"
				if vol.contentType == ContentTypeFS {
					fileName = fmt.Sprintf("%s-config.bin", snap)
				}
			}

			target := fmt.Sprintf("backup/%s/%s", prefix, fileName)
			err := sendToFile(cur, parent, target)
			if err != nil {
				return err
			}

			finalParent = cur
		}
	}

	// Make a temporary copy of the container.
	sourceVolume := vol.MountPath()
	containersPath := GetVolumeMountPath(d.name, vol.volType, "")

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, "backup.")
	if err != nil {
		return errors.Wrapf(err, "Failed to create temporary directory under '%s'", containersPath)
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0100)
	if err != nil {
		return errors.Wrapf(err, "Failed to chmod '%s'", tmpContainerMntPoint)
	}

	// Create the read-only snapshot.
	targetVolume := fmt.Sprintf("%s/.backup", tmpContainerMntPoint)
	err = d.snapshotSubvolume(sourceVolume, targetVolume, true, true)
	if err != nil {
		return err
	}
	defer d.deleteSubvolume(targetVolume, true)

	// Dump the container to a file.
	fileName := "container.bin"
	if vol.volType == VolumeTypeVM {
		if vol.contentType == ContentTypeFS {
			fileName = "virtual-machine-config.bin"
		} else {
			fileName = "virtual-machine.bin"
		}
	}

	// Dump the container to a file.
	err = sendToFile(targetVolume, finalParent, fmt.Sprintf("backup/%s", fileName))
	if err != nil {
		return err
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *btrfs) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	srcPath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	snapPath := snapVol.MountPath()

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	return d.snapshotSubvolume(srcPath, snapPath, true, true)
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *btrfs) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	snapPath := snapVol.MountPath()

	// Delete the snapshot.
	err := d.deleteSubvolume(snapPath, true)
	if err != nil {
		return err
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	return nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *btrfs) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	snapPath := snapVol.MountPath()
	return mountReadOnly(snapPath, snapPath)
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *btrfs) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	snapPath := snapVol.MountPath()
	return forceUnmount(snapPath)
}

// VolumeSnapshots returns a list of snapshots for the volume.
func (d *btrfs) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return genericVFSVolumeSnapshots(d, vol, op)
}

// RestoreVolume restores a volume from a snapshot.
func (d *btrfs) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	// Create a backup so we can revert.
	backupSubvolume := fmt.Sprintf("%s%s", vol.MountPath(), tmpVolSuffix)
	err := os.Rename(vol.MountPath(), backupSubvolume)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename '%s' to '%s'", vol.MountPath(), backupSubvolume)
	}

	// Setup revert logic.
	undoSnapshot := true
	defer func() {
		if undoSnapshot {
			os.Rename(vol.MountPath(), backupSubvolume)
		}
	}()

	// Restore the snapshot.
	source := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapshotName))
	err = d.snapshotSubvolume(source, vol.MountPath(), false, true)
	if err != nil {
		return err
	}

	undoSnapshot = false

	// Remove the backup subvolume.
	return d.deleteSubvolume(backupSubvolume, true)
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *btrfs) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	return genericVFSRenameVolumeSnapshot(d, snapVol, newSnapshotName, op)
}
