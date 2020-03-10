package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
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

	for _, snapName := range volSrcArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			var wrapper *ioprogress.ProgressTracker
			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			path := shared.AddSlash(mountPath)
			return rsync.Send(snapshot.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, s.OS.ExecPath)
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to recipient (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		path := shared.AddSlash(mountPath)
		return rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, s.OS.ExecPath)
	}, op)
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
func genericVFSBackupVolume(d Driver, vol Volume, targetPath string, snapshots bool, op *operations.Operation) error {
	bwlimit := d.Config()["rsync.bwlimit"]

	// Backups only implemented for containers currently.
	if vol.volType != VolumeTypeContainer {
		return ErrNotImplemented
	}
	// Handle snapshots.
	if snapshots {
		snapshotsPath := filepath.Join(targetPath, "snapshots")

		// List the snapshots.
		snapshots, err := vol.Snapshots(op)
		if err != nil {
			return err
		}

		// Create the snapshot path.
		if len(snapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return errors.Wrapf(err, "Failed to create directory '%s'", snapshotsPath)
			}
		}

		for _, snapshot := range snapshots {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snapshot.Name())
			target := filepath.Join(snapshotsPath, snapName)

			// Copy the snapshot.
			err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
				_, err := rsync.LocalCopy(mountPath, target, bwlimit, true)
				if err != nil {
					return err
				}

				return nil
			}, op)
			if err != nil {
				return err
			}
		}
	}

	// Copy the parent volume itself.
	target := filepath.Join(targetPath, "container")
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		_, err := rsync.LocalCopy(mountPath, target, bwlimit, true)
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	return nil
}

// genericVFSResizeBlockFile resizes an existing block file to the specified size. Returns true if resize took
// place, false if not. Both requested size and existing file size are rounded to nearest block size using
// roundVolumeBlockFileSizeBytes() before decision whether to resize is taken.
func genericVFSResizeBlockFile(filePath, size string) (bool, error) {
	if size == "" || size == "0" {
		return false, fmt.Errorf("Size cannot be zero")
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	oldSizeBytes := fi.Size()

	// Round the supplied size the same way the block files created are so its accurate comparison.
	newSizeBytes, err := roundVolumeBlockFileSizeBytes(size)
	if err != nil {
		return false, err
	}

	if newSizeBytes < oldSizeBytes {
		return false, fmt.Errorf("You cannot shrink block volumes")
	}

	if newSizeBytes == oldSizeBytes {
		return false, nil
	}

	// Resize block file.
	err = ensureVolumeBlockFile(filePath, size)
	if err != nil {
		return false, err
	}

	return true, nil
}
