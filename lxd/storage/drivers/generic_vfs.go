package drivers

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/instancewriter"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// genericVFSGetResources is a generic GetResources implementation for VFS-only drivers.
func genericVFSGetResources(d Driver) (*api.ResourcesStoragePool, error) {
	// Get the VFS information
	st, err := shared.Statvfs(GetPoolMountPath(d.Name()))
	if err != nil {
		return nil, err
	}

	// Fill in the struct
	res := api.ResourcesStoragePool{}
	res.Space.Total = st.Blocks * uint64(st.Bsize)
	res.Space.Used = (st.Blocks - st.Bfree) * uint64(st.Bsize)

	// Some filesystems don't report inodes since they allocate them
	// dynamically e.g. btrfs.
	if st.Files > 0 {
		res.Inodes.Total = st.Files
		res.Inodes.Used = st.Files - st.Ffree
	}

	return &res, nil
}

// genericVFSRenameVolume is a generic RenameVolume implementation for VFS-only drivers.
func genericVFSRenameVolume(d Driver, vol Volume, newVolName string, op *operations.Operation) error {
	if vol.IsSnapshot() {
		return fmt.Errorf("Volume must not be a snapshot")
	}

	// Rename the volume itself.
	srcVolumePath := GetVolumeMountPath(d.Name(), vol.volType, vol.name)
	dstVolumePath := GetVolumeMountPath(d.Name(), vol.volType, newVolName)

	revertRename := true
	if shared.PathExists(srcVolumePath) {
		err := os.Rename(srcVolumePath, dstVolumePath)
		if err != nil {
			return errors.Wrapf(err, "Failed to rename '%s' to '%s'", srcVolumePath, dstVolumePath)
		}

		defer func() {
			if !revertRename {
				return
			}

			os.Rename(dstVolumePath, srcVolumePath)
		}()
	}

	// And if present, the snapshots too.
	srcSnapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, vol.name)
	dstSnapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, newVolName)

	if shared.PathExists(srcSnapshotDir) {
		err := os.Rename(srcSnapshotDir, dstSnapshotDir)
		if err != nil {
			return errors.Wrapf(err, "Failed to rename '%s' to '%s'", srcSnapshotDir, dstSnapshotDir)
		}
	}

	revertRename = false
	return nil
}

// genericVFSVolumeSnapshots is a generic VolumeSnapshots implementation for VFS-only drivers.
func genericVFSVolumeSnapshots(d Driver, vol Volume, op *operations.Operation) ([]string, error) {
	snapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, vol.name)
	snapshots := []string{}

	ents, err := ioutil.ReadDir(snapshotDir)
	if err != nil {
		// If the snapshots directory doesn't exist, there are no snapshots.
		if os.IsNotExist(err) {
			return snapshots, nil
		}

		return nil, errors.Wrapf(err, "Failed to list directory '%s'", snapshotDir)
	}

	for _, ent := range ents {
		fileInfo, err := os.Stat(filepath.Join(snapshotDir, ent.Name()))
		if err != nil {
			return nil, err
		}

		if !fileInfo.IsDir() {
			continue
		}

		snapshots = append(snapshots, ent.Name())
	}

	return snapshots, nil
}

// genericVFSRenameVolumeSnapshot is a generic RenameVolumeSnapshot implementation for VFS-only drivers.
func genericVFSRenameVolumeSnapshot(d Driver, snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	if !snapVol.IsSnapshot() {
		return fmt.Errorf("Volume must be a snapshot")
	}

	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	oldPath := snapVol.MountPath()
	newPath := GetVolumeMountPath(d.Name(), snapVol.volType, GetSnapshotVolumeName(parentName, newSnapshotName))

	err := os.Rename(oldPath, newPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename '%s' to '%s'", oldPath, newPath)
	}

	return nil
}

// genericVFSMigrateVolume is a generic MigrateVolume implementation for VFS-only drivers.
func genericVFSMigrateVolume(d Driver, s *state.State, vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	bwlimit := d.Config()["rsync.bwlimit"]

	var rsyncArgs []string

	// For VM volumes, if the root volume disk path is a file image in the volume's mount path then exclude it
	// from being transferred via rsync during the filesystem volume transfer, as it will be transferred later
	// using a different method.
	if vol.IsVMBlock() {
		if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC {
			return ErrNotSupported
		}

		diskPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return errors.Wrapf(err, "Error getting VM block volume disk path")
		}

		if strings.HasPrefix(diskPath, vol.MountPath()) {
			rsyncArgs = []string{"--exclude", filepath.Base(diskPath)}
		}
	} else if vol.contentType == ContentTypeBlock && volSrcArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC || vol.contentType == ContentTypeFS && volSrcArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

	// Define function to send a filesystem volume.
	sendFSVol := func(vol Volume, conn io.ReadWriteCloser, mountPath string) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		path := shared.AddSlash(mountPath)

		d.Logger().Debug("Sending filesystem volume", log.Ctx{"volName": vol.name, "path": path})
		return rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, s.OS.ExecPath, rsyncArgs...)
	}

	// Define function to send a block volume.
	sendBlockVol := func(vol Volume, conn io.ReadWriteCloser) error {
		// Close when done to indicate to target side we are finished sending this volume.
		defer conn.Close()

		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "block_progress", vol.name)
		}

		path, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return errors.Wrapf(err, "Error getting VM block volume disk path")
		}

		from, err := os.Open(path)
		if err != nil {
			return errors.Wrapf(err, "Error opening file for reading %q", path)
		}
		defer from.Close()

		// Setup progress tracker.
		fromPipe := io.ReadCloser(from)
		if wrapper != nil {
			fromPipe = &ioprogress.ProgressReader{
				ReadCloser: fromPipe,
				Tracker:    wrapper,
			}
		}

		d.Logger().Debug("Sending block volume", log.Ctx{"volName": vol.name, "path": path})
		_, err = io.Copy(conn, fromPipe)
		if err != nil {
			return errors.Wrapf(err, "Error copying %q to migration connection", path)
		}

		return nil
	}

	// Send all snapshots to target.
	for _, snapName := range volSrcArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		// Send snapshot to target (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			if vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom {
				err := sendFSVol(snapshot, conn, mountPath)
				if err != nil {
					return err
				}
			}

			if vol.IsVMBlock() || vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom {
				err = sendBlockVol(snapshot, conn)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to target (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		if vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom {
			err := sendFSVol(vol, conn, mountPath)
			if err != nil {
				return err
			}
		}

		if vol.IsVMBlock() || vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom {
			err := sendBlockVol(vol, conn)
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
}

// genericVFSCreateVolumeFromMigration receives a volume and its snapshots over a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericVFSCreateVolumeFromMigration(d Driver, initVolume func(vol Volume) (func(), error), vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// Check migration transport type matches volume type.
	if vol.contentType == ContentTypeBlock {
		if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC {
			return ErrNotSupported
		}
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

	revert := revert.New()
	defer revert.Fail()

	// Create the main volume if not refreshing.
	if !volTargetArgs.Refresh {
		err := d.CreateVolume(vol, preFiller, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolume(vol, op) })
	}

	recvFSVol := func(volName string, conn io.ReadWriteCloser, path string) error {
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", volName)
		}

		d.Logger().Debug("Receiving filesystem volume", log.Ctx{"volName": volName, "path": path})
		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}

	recvBlockVol := func(volName string, conn io.ReadWriteCloser, path string) error {
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "block_progress", volName)
		}

		to, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return errors.Wrapf(err, "Error opening file for writing %q", path)
		}
		defer to.Close()

		// Setup progress tracker.
		fromPipe := io.ReadCloser(conn)
		if wrapper != nil {
			fromPipe = &ioprogress.ProgressReader{
				ReadCloser: fromPipe,
				Tracker:    wrapper,
			}
		}

		d.Logger().Debug("Receiving block volume", log.Ctx{"volName": volName, "path": path})
		_, err = io.Copy(to, fromPipe)
		if err != nil {
			return errors.Wrapf(err, "Error copying from migration connection to %q", path)
		}

		return nil
	}

	// Ensure the volume is mounted.
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var err error

		// Setup paths to the main volume. We will receive each snapshot to these paths and then create
		// a snapshot of the main volume for each one.
		path := shared.AddSlash(mountPath)
		pathBlock := ""

		if vol.IsVMBlock() || vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom {
			pathBlock, err = d.GetVolumeDiskPath(vol)
			if err != nil {
				return errors.Wrapf(err, "Error getting VM block volume disk path")
			}
		}

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)

			if snapVol.contentType != ContentTypeBlock || snapVol.volType != VolumeTypeCustom { // Receive the filesystem snapshot first (as it is sent first).
				err = recvFSVol(snapVol.name, conn, path)
				if err != nil {
					return err
				}
			}

			// Receive the block snapshot next (if needed).
			if vol.IsVMBlock() || vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom {
				err = recvBlockVol(snapVol.name, conn, pathBlock)
				if err != nil {
					return err
				}
			}

			// Create the snapshot itself.
			d.Logger().Debug("Creating snapshot", log.Ctx{"volName": snapVol.Name()})
			err = d.CreateVolumeSnapshot(snapVol, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			revert.Add(func() {
				d.DeleteVolumeSnapshot(snapVol, op)
			})
		}

		// Run volume-specific init logic.
		if initVolume != nil {
			_, err := initVolume(vol)
			if err != nil {
				return err
			}
		}

		if vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom {
			// Receive main volume.
			err = recvFSVol(vol.name, conn, path)
			if err != nil {
				return err
			}
		}

		// Receive the final main volume sync if needed.
		if volTargetArgs.Live && (vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom) {
			d.Logger().Debug("Starting main volume final sync", log.Ctx{"volName": vol.name, "path": path})
			err = recvFSVol(vol.name, conn, path)
			if err != nil {
				return err
			}
		}

		// Run EnsureMountPath after mounting and syncing to ensure the mounted directory has the
		// correct permissions set.
		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		// Receive the block volume next (if needed).
		if vol.IsVMBlock() || vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom {
			err = recvBlockVol(vol.name, conn, pathBlock)
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// genericVFSHasVolume is a generic HasVolume implementation for VFS-only drivers.
func genericVFSHasVolume(vol Volume) bool {
	if shared.PathExists(vol.MountPath()) {
		return true
	}

	return false
}

// genericVFSGetVolumeDiskPath is a generic GetVolumeDiskPath implementation for VFS-only drivers.
func genericVFSGetVolumeDiskPath(vol Volume) (string, error) {
	if vol.contentType != ContentTypeBlock {
		return "", ErrNotSupported
	}

	return filepath.Join(vol.MountPath(), "root.img"), nil
}

// genericVFSBackupVolume is a generic BackupVolume implementation for VFS-only drivers.
func genericVFSBackupVolume(d Driver, vol Volume, tarWriter *instancewriter.InstanceTarWriter, snapshots bool, op *operations.Operation) error {
	// Define a function that can copy a volume into the backup target location.
	backupVolume := func(v Volume, prefix string) error {
		return v.MountTask(func(mountPath string, op *operations.Operation) error {
			// Reset hard link cache as we are copying a new volume (instance or snapshot).
			tarWriter.ResetHardLinkMap()

			if v.IsVMBlock() {
				blockPath, err := d.GetVolumeDiskPath(v)
				if err != nil {
					return errors.Wrapf(err, "Error getting VM block volume disk path")
				}

				var blockDiskSize int64
				var exclude []string

				// Get size of disk block device for tarball header.
				blockDiskSize, err = BlockDiskSizeBytes(blockPath)
				if err != nil {
					return errors.Wrapf(err, "Error getting block device size %q", blockPath)
				}

				if !shared.IsBlockdevPath(blockPath) {
					// Exclude the VM root disk path from the config volume backup part.
					// We will read it as a block device later instead.
					exclude = append(exclude, blockPath)
				}

				d.Logger().Debug("Copying virtual machine config volume", log.Ctx{"sourcePath": mountPath, "prefix": prefix})
				err = filepath.Walk(mountPath, func(srcPath string, fi os.FileInfo, err error) error {
					if err != nil {
						return err
					}

					// Skip any exluded files.
					if shared.StringInSlice(srcPath, exclude) {
						return nil
					}

					name := filepath.Join(prefix, strings.TrimPrefix(srcPath, mountPath))
					err = tarWriter.WriteFile(name, srcPath, fi, false)
					if err != nil {
						return errors.Wrapf(err, "Error adding %q as %q to tarball", srcPath, name)
					}

					return nil
				})
				if err != nil {
					return err
				}

				name := fmt.Sprintf("%s.img", prefix)
				d.Logger().Debug("Copying virtual machine block volume", log.Ctx{"sourcePath": blockPath, "file": name, "size": blockDiskSize})
				from, err := os.Open(blockPath)
				if err != nil {
					return errors.Wrapf(err, "Error opening file for reading %q", blockPath)
				}
				defer from.Close()

				fi := instancewriter.FileInfo{
					FileName:    name,
					FileSize:    blockDiskSize,
					FileMode:    0600,
					FileModTime: time.Now(),
				}

				err = tarWriter.WriteFileFromReader(from, &fi)
				if err != nil {
					return errors.Wrapf(err, "Error copying %q as %q to tarball", blockPath, name)
				}
			} else {
				d.Logger().Debug("Copying container filesystem volume", log.Ctx{"sourcePath": mountPath, "prefix": prefix})
				return filepath.Walk(mountPath, func(srcPath string, fi os.FileInfo, err error) error {
					if err != nil {
						if os.IsNotExist(err) {
							logger.Warnf("File vanished during export: %q, skipping", srcPath)
							return nil
						}

						return errors.Wrapf(err, "Error walking file during export: %q", srcPath)
					}

					name := filepath.Join(prefix, strings.TrimPrefix(srcPath, mountPath))

					// Write the file to the tarball with ignoreGrowth enabled so that if the
					// source file grows during copy we only copy up to the original size.
					// This means that the file in the tarball may be inconsistent.
					err = tarWriter.WriteFile(name, srcPath, fi, true)
					if err != nil {
						return errors.Wrapf(err, "Error adding %q as %q to tarball", srcPath, name)
					}

					return nil
				})
			}

			return nil
		}, op)
	}

	// Handle snapshots.
	if snapshots {
		snapshotsPrefix := "backup/snapshots"
		if vol.IsVMBlock() {
			snapshotsPrefix = "backup/virtual-machine-snapshots"
		}

		// List the snapshots.
		snapshots, err := vol.Snapshots(op)
		if err != nil {
			return err
		}

		for _, snapshot := range snapshots {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snapshot.Name())
			prefix := filepath.Join(snapshotsPrefix, snapName)
			err := backupVolume(snapshot, prefix)
			if err != nil {
				return err
			}
		}
	}

	// Copy the main volume itself.
	prefix := "backup/container"
	if vol.IsVMBlock() {
		prefix = "backup/virtual-machine"
	}

	err := backupVolume(vol, prefix)
	if err != nil {
		return err
	}

	return nil
}

// genericVFSBackupUnpack unpacks a non-optimized backup tarball through a storage driver.
// Returns a post hook function that should be called once the database entries for the restored backup have been
// created and a revert function that can be used to undo the actions this function performs should something
// subsequently fail.
func genericVFSBackupUnpack(d Driver, vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	// Define function to unpack a volume from a backup tarball file.
	unpackVolume := func(r io.ReadSeeker, tarArgs []string, unpacker []string, srcPrefix string, mountPath string) error {
		volTypeName := "container"
		if vol.IsVMBlock() {
			volTypeName = "virtual machine"
		}

		// Clear the volume ready for unpack.
		err := wipeDirectory(mountPath)
		if err != nil {
			return errors.Wrapf(err, "Error clearing volume before unpack")
		}

		// Prepare tar arguments.
		srcParts := strings.Split(srcPrefix, string(os.PathSeparator))
		args := append(tarArgs, []string{
			"-",
			"--xattrs-include=*",
			fmt.Sprintf("--strip-components=%d", len(srcParts)),
			"-C", mountPath, srcPrefix,
		}...)

		// Extract filesystem volume.
		d.Logger().Debug(fmt.Sprintf("Unpacking %s filesystem volume", volTypeName), log.Ctx{"source": srcPrefix, "target": mountPath})
		srcData.Seek(0, 0)
		err = shared.RunCommandWithFds(r, nil, "tar", args...)
		if err != nil {
			return errors.Wrapf(err, "Error starting unpack")
		}

		// Extract block file to block volume if VM.
		if vol.IsVMBlock() {
			targetPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			srcFile := fmt.Sprintf("%s.img", srcPrefix)
			d.Logger().Debug("Unpacking virtual machine block volume", log.Ctx{"source": srcFile, "target": targetPath})

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
					// Open block file (use O_CREATE to support drivers that use image files).
					to, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
					if err != nil {
						return errors.Wrapf(err, "Error opening file for writing %q", targetPath)
					}
					defer to.Close()

					// Restore original size of volume from raw block backup file size.
					d.Logger().Debug("Setting volume size from source", log.Ctx{"source": srcFile, "target": targetPath, "size": hdr.Size})

					// Allow potentially destructive resize of volume as we are going to be
					// overwriting it entirely anyway. This allows shrinking of block volumes.
					vol.allowUnsafeResize = true
					err = d.SetVolumeQuota(vol, fmt.Sprintf("%d", hdr.Size), op)
					if err != nil {
						return err
					}

					_, err = io.Copy(to, tr)
					if err != nil {
						return err
					}

					cancelFunc()
					return nil
				}
			}

			return fmt.Errorf("Could not find %q", srcFile)
		}

		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, unpacker, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	if d.HasVolume(vol) {
		return nil, nil, fmt.Errorf("Cannot restore volume, already exists on target")
	}

	// Create new empty volume.
	err = d.CreateVolume(vol, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	revert.Add(func() { d.DeleteVolume(vol, op) })

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(d.Name(), vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	backupSnapshotsPrefix := "backup/snapshots"
	if vol.IsVMBlock() {
		backupSnapshotsPrefix = "backup/virtual-machine-snapshots"
	}

	for _, snapName := range snapshots {
		err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
			backupSnapshotPrefix := fmt.Sprintf("%s/%s", backupSnapshotsPrefix, snapName)
			return unpackVolume(srcData, tarArgs, unpacker, backupSnapshotPrefix, mountPath)
		}, op)
		if err != nil {
			return nil, nil, err
		}

		snapVol, err := vol.NewSnapshot(snapName)
		if err != nil {
			return nil, nil, err
		}

		d.Logger().Debug("Creating volume snapshot", log.Ctx{"snapshotName": snapVol.Name()})
		err = d.CreateVolumeSnapshot(snapVol, op)
		if err != nil {
			return nil, nil, err
		}
		revert.Add(func() { d.DeleteVolumeSnapshot(snapVol, op) })
	}

	// Mount main volume and leave mounted (as is needed during backup.yaml generation during latter parts of
	// the backup restoration process).
	ourMount, err := d.MountVolume(vol, op)
	if err != nil {
		return nil, nil, err
	}

	// Create a post hook function that will be called at the end of the backup restore process to unmount
	// the volume if needed.
	postHook := func(vol Volume) error {
		if ourMount {
			d.UnmountVolume(vol, op)
		}

		return nil
	}

	backupPrefix := "backup/container"
	if vol.IsVMBlock() {
		backupPrefix = "backup/virtual-machine"
	}

	mountPath := vol.MountPath()
	err = unpackVolume(srcData, tarArgs, unpacker, backupPrefix, mountPath)
	if err != nil {
		return nil, nil, err
	}

	// Run EnsureMountPath after mounting and unpacking to ensure the mounted directory has the
	// correct permissions set.
	err = vol.EnsureMountPath()
	if err != nil {
		return nil, nil, err
	}

	revertExternal := revert.Clone() // Clone before calling revert.Success() so we can return the Fail func.
	revert.Success()
	return postHook, revertExternal.Fail, nil
}

// genericVFSCopyVolume copies a volume and its snapshots using a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericVFSCopyVolume(d Driver, initVolume func(vol Volume) (func(), error), vol Volume, srcVol Volume, srcSnapshots []Volume, refresh bool, op *operations.Operation) error {
	if vol.contentType != srcVol.contentType {
		return fmt.Errorf("Content type of source and target must be the same")
	}

	bwlimit := d.Config()["rsync.bwlimit"]

	revert := revert.New()
	defer revert.Fail()

	// Create the main volume if not refreshing.
	if !refresh {
		err := d.CreateVolume(vol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolume(vol, op) })
	}

	// Ensure the volume is mounted.
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if len(srcSnapshots) > 0 && !srcVol.IsSnapshot() {
			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := shared.InstanceGetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err := srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
					if err != nil {
						return err
					}

					if srcSnapshot.IsVMBlock() {
						srcDevPath, err := d.GetVolumeDiskPath(srcSnapshot)
						if err != nil {
							return err
						}

						targetDevPath, err := d.GetVolumeDiskPath(vol)
						if err != nil {
							return err
						}

						err = copyDevice(srcDevPath, targetDevPath)
						if err != nil {
							return err
						}
					}

					return nil
				}, op)
				if err != nil {
					return err
				}

				fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
				snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

				// Create the snapshot itself.
				d.Logger().Debug("Creating snapshot", log.Ctx{"volName": snapVol.Name()})
				err = d.CreateVolumeSnapshot(snapVol, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revert.Add(func() {
					d.DeleteVolumeSnapshot(snapVol, op)
				})
			}
		}

		// Run volume-specific init logic.
		if initVolume != nil {
			_, err := initVolume(vol)
			if err != nil {
				return err
			}
		}

		// Copy source to destination (mounting each volume if needed).
		err := srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
			if err != nil {
				return err
			}

			if srcVol.IsVMBlock() {
				srcDevPath, err := d.GetVolumeDiskPath(srcVol)
				if err != nil {
					return err
				}

				targetDevPath, err := d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}

				err = copyDevice(srcDevPath, targetDevPath)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}

		// Run EnsureMountPath after mounting and copying to ensure the mounted directory has the
		// correct permissions set.
		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
