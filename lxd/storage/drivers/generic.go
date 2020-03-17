package drivers

import (
	"fmt"
	"io"
	"os"

	"github.com/pkg/errors"

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

// genericCreateVolumeFromMigration receives a volume and its snapshots over a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericCreateVolumeFromMigration(d Driver, initVolume func(vol Volume) (func(), error), vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// Check migration transport type matches volume type.
	if vol.IsVMBlock() {
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

		if vol.IsVMBlock() {
			pathBlock, err = d.GetVolumeDiskPath(vol)
			if err != nil {
				return errors.Wrapf(err, "Error getting VM block volume disk path")
			}
		}

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)

			// Receive the filesystem snapshot first (as it is sent first).
			err = recvFSVol(snapVol.name, conn, path)
			if err != nil {
				return err
			}

			// Receive the block snapshot next (if needed).
			if vol.IsVMBlock() {
				err = recvBlockVol(snapVol.name, conn, pathBlock)
				if err != nil {
					return err
				}
			}

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

		// Run volume-specific init logic.
		if initVolume != nil {
			_, err := initVolume(vol)
			if err != nil {
				return err
			}
		}

		// Receive main volume.
		err = recvFSVol(vol.name, conn, path)
		if err != nil {
			return err
		}

		// Receive the final main volume sync if needed.
		if volTargetArgs.Live {
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
		if vol.IsVMBlock() {
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

// genericBackupUnpack unpacks a non-optimized backup tarball through a storage driver.
// Returns a post hook function that should be called once the database entries for the restored backup have been
// created and a revert function that can be used to undo the actions this function performs should something
// subsequently fail.
func genericBackupUnpack(d Driver, vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	revert := revert.New()
	defer revert.Fail()

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
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

	for _, snapName := range snapshots {
		err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
			// Prepare tar arguments.
			args := append(tarArgs, []string{
				"-",
				"--recursive-unlink",
				"--xattrs-include=*",
				"--strip-components=3",
				"-C", mountPath, fmt.Sprintf("backup/snapshots/%s", snapName),
			}...)

			// Extract snapshot.
			srcData.Seek(0, 0)
			err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
			if err != nil {
				return err
			}

			return nil
		}, op)
		if err != nil {
			return nil, nil, err
		}

		snapVol, err := vol.NewSnapshot(snapName)
		if err != nil {
			return nil, nil, err
		}

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
