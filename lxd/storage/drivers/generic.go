package drivers

import (
	"fmt"
	"io"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
)

// genericCopyVolume copies a volume and its snapshots using a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericCopyVolume(d Driver, initVolume func(vol Volume) (func(), error), vol Volume, srcVol Volume, srcSnapshots []Volume, refresh bool, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS || srcVol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
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
					return err
				}, op)
				if err != nil {
					return err
				}

				fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
				snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

				// Create the snapshot itself.
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
		return srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
			return err
		}, op)
	}, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// genericCreateVolumeFromMigration receives a volume and its snapshots over a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericCreateVolumeFromMigration(d Driver, initVolume func(vol Volume) (func(), error), vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
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

	// Ensure the volume is mounted.
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		path := shared.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot
			var wrapper *ioprogress.ProgressTracker
			if volTargetArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapName)
			}

			d.Logger().Debug("Receiving volume", log.Ctx{"volume": vol.name, "snapshot": snapName, "path": path})
			err := rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
			if err != nil {
				return err
			}

			// Create the snapshot itself.
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)

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

		// Receive the main volume from sender.
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		d.Logger().Debug("Receiving volume", log.Ctx{"volume": vol.name, "path": path})
		err := rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
		if err != nil {
			return err
		}

		// Receive the final main volume sync if needed.
		if volTargetArgs.Live {
			if volTargetArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
			}

			d.Logger().Debug("Receiving volume (final stage)", log.Ctx{"vol": vol.name, "path": path})
			err = rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
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

// genericBackupUnpack unpacks a non-optimized backup tarball through a storage driver.
func genericBackupUnpack(d Driver, vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	revert := revert.New()
	defer revert.Fail()

	// Define a revert function that will be used both to revert if an error occurs inside this
	// function but also return it for use from the calling functions if no error internally.
	revertHook := func() {
		for _, snapName := range snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		// And lastly the main volume.
		d.DeleteVolume(vol, op)
	}

	// Only execute the revert function if we have had an error internally.
	revert.Add(revertHook)

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	// Create the main volume.
	err = d.CreateVolume(vol, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(d.Name(), vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snapName := range snapshots {
		// Prepare tar arguments.
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--xattrs-include=*",
			"--strip-components=3",
			"-C", vol.MountPath(), fmt.Sprintf("backup/snapshots/%s", snapName),
		}...)

		// Extract snapshots.
		srcData.Seek(0, 0)
		err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
		if err != nil {
			return nil, nil, err
		}

		fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
		snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)
		err = d.CreateVolumeSnapshot(snapVol, op)
		if err != nil {
			return nil, nil, err
		}
	}

	// Prepare tar extraction arguments.
	args := append(tarArgs, []string{
		"-",
		"--recursive-unlink",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", vol.MountPath(), "backup/container",
	}...)

	// Extract instance.
	srcData.Seek(0, 0)
	err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
	if err != nil {
		return nil, nil, err
	}

	revert.Success()
	return nil, revertHook, nil
}
