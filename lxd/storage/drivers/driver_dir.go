package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/storage/quota"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/units"
)

type dir struct {
	common
}

// Info returns info about the driver and its environment.
func (d *dir) Info() Info {
	return Info{
		Name:               "dir",
		Version:            "1",
		OptimizedImages:    false,
		PreservesInodes:    false,
		Remote:             false,
		VolumeTypes:        []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:       false,
		RunningQuotaResize: true,
	}
}

func (d *dir) Create() error {
	// WARNING: The Create() function cannot rely on any of the struct attributes being set.

	// Set default source if missing.
	if d.config["source"] == "" {
		d.config["source"] = GetPoolMountPath(d.name)
	}

	if !shared.PathExists(d.config["source"]) {
		return fmt.Errorf("Source path '%s' doesn't exist", d.config["source"])
	}

	// Check that if within LXD_DIR, we're at our expected spot.
	cleanSource := filepath.Clean(d.config["source"])
	if strings.HasPrefix(cleanSource, shared.VarPath()) && cleanSource != GetPoolMountPath(d.name) {
		return fmt.Errorf("Source path '%s' is within the LXD directory", d.config["source"])
	}

	// Check that the path is currently empty.
	isEmpty, err := shared.PathIsEmpty(d.config["source"])
	if err != nil {
		return err
	}

	if !isEmpty {
		return fmt.Errorf("Source path '%s' isn't empty", d.config["source"])
	}

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *dir) Delete(op *operations.Operation) error {
	// On delete, wipe everything in the directory.
	err := wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Unmount the path.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

// Mount mounts the storage pool.
func (d *dir) Mount() (bool, error) {
	path := GetPoolMountPath(d.name)

	// Check if we're dealing with an external mount.
	if d.config["source"] == path {
		return false, nil
	}

	// Check if already mounted.
	if sameMount(d.config["source"], path) {
		return false, nil
	}

	// Setup the bind-mount.
	err := tryMount(d.config["source"], path, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *dir) Unmount() (bool, error) {
	path := GetPoolMountPath(d.name)

	// Check if we're dealing with an external mount.
	if d.config["source"] == path {
		return false, nil
	}

	// Unmount until nothing is left mounted.
	return forceUnmount(path)
}

func (d *dir) GetResources() (*api.ResourcesStoragePool, error) {
	// Use the generic VFS resources.
	return vfsResources(GetPoolMountPath(d.name))
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *dir) GetVolumeUsage(volType VolumeType, volName string) (int64, error) {
	volPath := GetVolumeMountPath(d.name, volType, volName)
	ok, err := quota.Supported(volPath)
	if err != nil || !ok {
		return 0, nil
	}

	// Get the volume ID for the volume to access quota.
	volID, err := d.getVolID(volType, volName)
	if err != nil {
		return -1, err
	}

	projectID := d.quotaProjectID(volID)

	// Get project quota used.
	size, err := quota.GetProjectUsage(volPath, projectID)
	if err != nil {
		return -1, err
	}

	return size, nil
}

// ValidateVolume validates the supplied volume config.
func (d *dir) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *dir) HasVolume(volType VolumeType, volName string) bool {
	if shared.PathExists(GetVolumeMountPath(d.name, volType, volName)) {
		return true
	}

	return false
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *dir) CreateVolume(vol Volume, filler func(mountPath, rootBlockPath string) error, op *operations.Operation) error {
	volPath := vol.MountPath()
	err := vol.CreateMountPath()
	if err != nil {
		return err
	}

	// Extract specified size from pool or volume config.
	size := d.config["volume.size"]
	if vol.config["size"] != "" {
		size = vol.config["size"]
	}

	revertPath := true
	defer func() {
		if revertPath {
			os.RemoveAll(volPath)
		}
	}()

	// Create sparse loopback file if volume is block.
	rootBlockPath := ""
	if vol.contentType == ContentTypeBlock {
		// We expect the filler to copy the VM image into this path.
		rootBlockPath, _, err = d.GetVolumeDiskPath(vol.volType, vol.name)
		if err != nil {
			return err
		}
	} else {
		// Get the volume ID for the new volume, which is used to set project quota.
		volID, err := d.getVolID(vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Initialise the volume's quota using the volume ID.
		err = d.initQuota(volPath, volID)
		if err != nil {
			return err
		}

		defer func() {
			if revertPath {
				d.deleteQuota(volPath, volID)
			}
		}()

		// Set the quota.
		err = d.setQuota(volPath, volID, size)
		if err != nil {
			return err
		}
	}

	// Run the volume filler function if supplied.
	if filler != nil {
		err = filler(volPath, rootBlockPath)
		if err != nil {
			return err
		}
	}

	// If we are creating a block volume, resize it to the requested size or 10GB.
	// We expect the filler function to have copied the qcow2 image to the rootBlockPath.
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
			_, err = shared.RunCommand("qemu-img", "resize", rootBlockPath, fmt.Sprintf("%d", blockSizeBytes))
			if err != nil {
				return fmt.Errorf("Failed resizing disk image %s to size %s: %v", rootBlockPath, blockSize, err)
			}
		} else {
			// If rootBlockPath doesn't exist, then there has been no filler function
			// supplied to create it from another source. So instead create an empty
			// volume (use for PXE booting a VM).
			_, err = shared.RunCommand("qemu-img", "create", "-f", "qcow2", rootBlockPath, fmt.Sprintf("%d", blockSizeBytes))
			if err != nil {
				return fmt.Errorf("Failed creating disk image %s as size %s: %v", rootBlockPath, blockSize, err)
			}
		}
	}

	revertPath = false
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *dir) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return fmt.Errorf("Migration type not supported")
	}

	bwlimit := d.config["rsync.bwlimit"]

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
			return rsync.Send(snapshot.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
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
		return rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
	}, op)
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *dir) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return fmt.Errorf("Migration type not supported")
	}

	// Get the volume ID for the new volumes, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return err
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err = vol.CreateMountPath()
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
			d.DeleteVolumeSnapshot(vol.volType, vol.name, snapName, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		path := shared.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot
			var wrapper *ioprogress.ProgressTracker
			if volTargetArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapName)
			}

			err = rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
			if err != nil {
				return err
			}

			// Create the snapshot itself.
			err = d.CreateVolumeSnapshot(vol.volType, vol.name, snapName, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			revertSnaps = append(revertSnaps, snapName)
		}

		// Initialise the volume's quota using the volume ID.
		err = d.initQuota(volPath, volID)
		if err != nil {
			return err
		}

		// Set the quota if specified in volConfig or pool config.
		err = d.setQuota(volPath, volID, vol.config["size"])
		if err != nil {
			return err
		}

		// Receive the main volume from sender.
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil
	return nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *dir) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS || srcVol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	bwlimit := d.config["rsync.bwlimit"]

	// Get the volume ID for the new volumes, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return err
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err = vol.CreateMountPath()
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
			d.DeleteVolumeSnapshot(vol.volType, vol.name, snapName, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if copySnapshots && !srcVol.IsSnapshot() {
			// Get the list of snapshots from the source.
			srcSnapshots, err := srcVol.Snapshots(op)
			if err != nil {
				return err
			}

			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := shared.ContainerGetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
					return err
				}, op)

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(vol.volType, vol.name, snapName, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}

		// Initialise the volume's quota using the volume ID.
		err = d.initQuota(volPath, volID)
		if err != nil {
			return err
		}

		// Set the quota if specified in volConfig or pool config.
		err = d.setQuota(volPath, volID, vol.config["size"])
		if err != nil {
			return err
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
func (d *dir) VolumeSnapshots(volType VolumeType, volName string, op *operations.Operation) ([]string, error) {
	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return nil, err
	}

	snapshots := []string{}

	ents, err := ioutil.ReadDir(snapshotDir)
	if err != nil {
		// If the snapshots directory doesn't exist, there are no snapshots.
		if os.IsNotExist(err) {
			return snapshots, nil
		}

		return nil, err
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

// UpdateVolume applies config changes to the volume.
func (d *dir) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if _, changed := changedConfig["size"]; changed {
		volID, err := d.getVolID(vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Set the quota if specified in volConfig or pool config.
		err = d.setQuota(vol.MountPath(), volID, changedConfig["size"])
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolume renames a volume and its snapshots.
func (d *dir) RenameVolume(volType VolumeType, volName string, newVolName string, op *operations.Operation) error {
	vol := NewVolume(d, d.name, volType, ContentTypeFS, volName, nil)

	// Create new snapshots directory.
	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, newVolName)
	if err != nil {
		return err
	}

	err = os.MkdirAll(snapshotDir, 0711)
	if err != nil {
		return err
	}

	type volRevert struct {
		oldPath string
		newPath string
	}

	// Create slice to record paths renamed if revert needed later.
	revertPaths := []volRevert{}
	defer func() {
		// Remove any paths rename if we are reverting.
		for _, vol := range revertPaths {
			os.Rename(vol.newPath, vol.oldPath)
		}

		// Remove the new snapshot directory if we are reverting.
		if len(revertPaths) > 0 {
			err = os.RemoveAll(snapshotDir)
		}
	}()

	// Rename any snapshots of the volume too.
	snapshots, err := vol.Snapshots(op)
	if err != nil {
		return err
	}

	for _, snapshot := range snapshots {
		oldPath := snapshot.MountPath()
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snapshot.name)
		newPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(newVolName, snapName))

		err := os.Rename(oldPath, newPath)
		if err != nil {
			return err
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath: oldPath,
			newPath: newPath,
		})
	}

	oldPath := GetVolumeMountPath(d.name, volType, volName)
	newPath := GetVolumeMountPath(d.name, volType, newVolName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	revertPaths = append(revertPaths, volRevert{
		oldPath: oldPath,
		newPath: newPath,
	})

	// Remove old snapshots directory.
	oldSnapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return err
	}
	err = os.RemoveAll(oldSnapshotDir)
	if err != nil {
		return err
	}

	revertPaths = nil
	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *dir) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	srcPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapshotName))
	if !shared.PathExists(srcPath) {
		return fmt.Errorf("Snapshot not found")
	}

	volPath := vol.MountPath()

	// Restore using rsync.
	bwlimit := d.config["rsync.bwlimit"]
	output, err := rsync.LocalCopy(srcPath, volPath, bwlimit, true)
	if err != nil {
		return fmt.Errorf("Failed to rsync volume: %s: %s", string(output), err)
	}

	return nil
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *dir) DeleteVolume(volType VolumeType, volName string, op *operations.Operation) error {
	snapshots, err := d.VolumeSnapshots(volType, volName, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volPath := GetVolumeMountPath(d.name, volType, volName)

	// If the volume doesn't exist, then nothing more to do.
	if !shared.PathExists(volPath) {
		return nil
	}

	// Get the volume ID for the volume, which is used to remove project quota.
	volID, err := d.getVolID(volType, volName)
	if err != nil {
		return err
	}

	// Remove the project quota.
	err = d.deleteQuota(volPath, volID)
	if err != nil {
		return err
	}

	// Remove the volume from the storage device.
	err = os.RemoveAll(volPath)
	if err != nil {
		return err
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	err = deleteParentSnapshotDirIfEmpty(d.name, volType, volName)
	if err != nil {
		return err
	}

	return nil
}

// MountVolume simulates mounting a volume. As dir driver doesn't have volumes to mount it returns
// false indicating that there is no need to issue an unmount.
func (d *dir) MountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error) {
	return false, nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *dir) MountVolumeSnapshot(volType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error) {
	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))
	return mountReadOnly(snapPath, snapPath)
}

// UnmountVolume simulates unmounting a volume. As dir driver doesn't have volumes to unmount it
// returns false indicating the volume was already unmounted.
func (d *dir) UnmountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error) {
	return false, nil
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *dir) UnmountVolumeSnapshot(volType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error) {
	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))
	return forceUnmount(snapPath)
}

// SetVolumeQuota sets the quota on the volume.
func (d *dir) SetVolumeQuota(volType VolumeType, volName, size string, op *operations.Operation) error {
	volPath := GetVolumeMountPath(d.name, volType, volName)
	volID, err := d.getVolID(volType, volName)
	if err != nil {
		return err
	}

	return d.setQuota(volPath, volID, size)
}

// quotaProjectID generates a project quota ID from a volume ID.
func (d *dir) quotaProjectID(volID int64) uint32 {
	return uint32(volID + 10000)
}

// initQuota initialises the project quota on the path. The volID generates a quota project ID.
func (d *dir) initQuota(path string, volID int64) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
		return nil
	}

	err = quota.SetProject(path, d.quotaProjectID(volID))
	if err != nil {
		return err
	}

	return nil
}

// setQuota sets the project quota on the path. The volID generates a quota project ID.
func (d *dir) setQuota(path string, volID int64, size string) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	// If size not specified in volume config, then use pool's default volume.size setting.
	if size == "" || size == "0" {
		size = d.config["volume.size"]
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		if sizeBytes > 0 {
			// Skipping quota as underlying filesystem doesn't suppport project quotas.
			d.logger.Warn("The backing filesystem doesn't support quotas, skipping quota", log.Ctx{"path": path})
		}
		return nil
	}

	err = quota.SetProjectQuota(path, d.quotaProjectID(volID), sizeBytes)
	if err != nil {
		return err
	}

	return nil
}

// deleteQuota removes the project quota for a volID from a path.
func (d *dir) deleteQuota(path string, volID int64) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
		return nil
	}

	err = quota.SetProject(path, 0)
	if err != nil {
		return err
	}

	err = quota.SetProjectQuota(path, d.quotaProjectID(volID), 0)
	if err != nil {
		return err
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *dir) CreateVolumeSnapshot(volType VolumeType, volName string, newSnapshotName string, op *operations.Operation) error {
	srcPath := GetVolumeMountPath(d.name, volType, volName)
	fullSnapName := GetSnapshotVolumeName(volName, newSnapshotName)
	snapVol := NewVolume(d, d.name, volType, ContentTypeFS, fullSnapName, nil)
	snapPath := snapVol.MountPath()

	// Create snapshot directory.
	err := snapVol.CreateMountPath()
	if err != nil {
		return err
	}

	revertPath := true
	defer func() {
		if revertPath {
			os.RemoveAll(snapPath)
		}
	}()

	bwlimit := d.config["rsync.bwlimit"]

	// Copy volume into snapshot directory.
	_, err = rsync.LocalCopy(srcPath, snapPath, bwlimit, true)
	if err != nil {
		return err
	}

	revertPath = false
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *dir) DeleteVolumeSnapshot(volType VolumeType, volName string, snapshotName string, op *operations.Operation) error {
	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))

	// Remove the snapshot from the storage device.
	err := os.RemoveAll(snapPath)
	if err != nil {
		return err
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, volType, volName)
	if err != nil {
		return err
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *dir) RenameVolumeSnapshot(volType VolumeType, volName string, snapshotName string, newSnapshotName string, op *operations.Operation) error {
	oldPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))
	newPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, newSnapshotName))
	err := os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	return nil
}
