package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/storage/quota"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
)

type dir struct {
	common
}

// Info returns info about the driver and its environment.
func (d *dir) Info() Info {
	return Info{
		Name:            "dir",
		Version:         "1",
		OptimizedImages: false,
		PreservesInodes: false,
		Usable:          true,
		Remote:          false,
		VolumeTypes:     []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer},
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

// ValidateVolume validates the supplied volume config.
func (d *dir) ValidateVolume(volConfig map[string]string, removeUnknownKeys bool) error {
	return d.validateVolume(volConfig, nil, removeUnknownKeys)
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
func (d *dir) CreateVolume(vol Volume, filler func(path string) error, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	volPath := vol.MountPath()

	// Get the volume ID for the new volume, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return err
	}

	err = os.MkdirAll(volPath, 0711)
	if err != nil {
		return err
	}

	revertPath := true
	defer func() {
		if revertPath {
			d.deleteQuota(volPath, volID)
			os.RemoveAll(volPath)
		}
	}()

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

	if filler != nil {
		err = filler(volPath)
		if err != nil {
			return err
		}
	}

	revertPath = false
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *dir) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
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
			wrapper := migration.ProgressTracker(op, "fs_progress", snapshot.name)
			path := shared.AddSlash(mountPath)
			return rsync.Send(snapshot.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to recipient (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		wrapper := migration.ProgressTracker(op, "fs_progress", vol.name)
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

	// Create slice of paths created if revert needed later.
	revertPaths := []string{}
	defer func() {
		// Remove any paths created if we are reverting.
		for _, path := range revertPaths {
			d.deleteQuota(path, volID)
			os.RemoveAll(path)
		}
	}()

	// Snapshots are sent first by the sender, so create these first.
	for _, snapName := range volTargetArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		snapPath := snapshot.MountPath()
		err = os.MkdirAll(snapPath, 0711)
		if err != nil {
			return err
		}

		revertPaths = append(revertPaths, snapPath)

		// Initialise the snapshot's quota with the parent volume's ID.
		err = d.initQuota(snapPath, volID)
		if err != nil {
			return err
		}

		// Receive snapshot from sender (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			wrapper := migration.ProgressTracker(op, "fs_progress", snapshot.name)
			path := shared.AddSlash(mountPath)
			return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
		}, op)
		if err != nil {
			return err
		}
	}

	volPath := vol.MountPath()

	// Finally the actual volume is sent by sender, so create that last.
	err = os.MkdirAll(volPath, 0711)
	if err != nil {
		return err
	}

	revertPaths = append(revertPaths, volPath)

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

	// Receive volume from sender (ensure local volume is mounted if needed).
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		wrapper := migration.ProgressTracker(op, "fs_progress", vol.name)
		path := shared.AddSlash(mountPath)
		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}, op)
	if err != nil {
		return err
	}

	revertPaths = nil
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

	// Create slice of paths created if revert needed later.
	revertPaths := []string{}
	defer func() {
		// Remove any paths created if we are reverting.
		for _, path := range revertPaths {
			d.deleteQuota(path, volID)
			os.RemoveAll(path)
		}
	}()

	// If copying snapshots is indicated, check the source isn't itself a snapshot.
	if copySnapshots && !srcVol.IsSnapshot() {
		srcSnapshots, err := srcVol.Snapshots(op)
		if err != nil {
			return err
		}

		for _, srcSnapshot := range srcSnapshots {
			_, snapName, _ := shared.ContainerGetParentAndSnapshotName(srcSnapshot.name)
			dstSnapshot, err := vol.NewSnapshot(snapName)
			if err != nil {
				return err
			}

			dstSnapPath := dstSnapshot.MountPath()
			err = os.MkdirAll(dstSnapPath, 0711)
			if err != nil {
				return err
			}

			revertPaths = append(revertPaths, dstSnapPath)

			// Initialise the snapshot's quota with the parent volume's ID.
			err = d.initQuota(dstSnapPath, volID)
			if err != nil {
				return err
			}

			err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
				return dstSnapshot.MountTask(func(dstMountPath string, op *operations.Operation) error {
					_, err = rsync.LocalCopy(srcMountPath, dstMountPath, bwlimit, true)
					return err
				}, op)
			}, op)
		}
	}

	volPath := vol.MountPath()
	err = os.MkdirAll(volPath, 0711)
	if err != nil {
		return err
	}

	revertPaths = append(revertPaths, volPath)

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
	err = srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
		return vol.MountTask(func(dstMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, dstMountPath, bwlimit, true)
			return err
		}, op)
	}, op)
	if err != nil {
		return err
	}

	revertPaths = nil // Don't revert.
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

	revertPaths = nil
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
	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return err
	}

	err = os.RemoveAll(snapshotDir)
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

// MountVolumeSnapshot simulates mounting a volume snapshot. As dir driver doesn't have volumes to
// mount it returns false indicating that there is no need to issue an unmount.
func (d *dir) MountVolumeSnapshot(volType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error) {
	return false, nil
}

// UnmountVolume simulates unmounting a volume. As dir driver doesn't have volumes to unmount it
// returns false indicating the volume was already unmounted.
func (d *dir) UnmountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error) {
	return false, nil
}

// UnmountVolume simulates unmounting a volume snapshot. As dir driver doesn't have volumes to
// unmount it returns false indicating the volume was already unmounted.
func (d *dir) UnmountVolumeSnapshot(volType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error) {
	return false, nil
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
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
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
	// Get the volume ID for the parent volume, which is used to set project quota on snapshot.
	volID, err := d.getVolID(volType, volName)
	if err != nil {
		return err
	}

	srcPath := GetVolumeMountPath(d.name, volType, volName)
	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, newSnapshotName))

	// Create snapshot directory.
	err = os.MkdirAll(snapPath, 0711)
	if err != nil {
		return err
	}

	revertPath := true
	defer func() {
		if revertPath {
			d.deleteQuota(snapPath, volID)
			os.RemoveAll(snapPath)
		}
	}()

	// Initialise the snapshot's quota with the parent volume's ID.
	err = d.initQuota(snapPath, volID)
	if err != nil {
		return err
	}

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
	// Get the volume ID for the parent volume, which is used to remove project quota.
	volID, err := d.getVolID(volType, volName)
	if err != nil {
		return err
	}

	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))

	// Remove the project quota.
	err = d.deleteQuota(snapPath, volID)
	if err != nil {
		return err
	}

	// Remove the snapshot from the storage device.
	err = os.RemoveAll(snapPath)
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
