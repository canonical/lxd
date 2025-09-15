package drivers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *pure) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-pure; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-pure; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-pure; group=volume-conf; key=size)
		// Default Pure Storage volume size rounded to 512B. The minimum size is 1MiB.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		"size": validate.Optional(validate.IsSize),
	}
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *pure) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	client := d.client()

	revert := revert.New()
	defer revert.Fail()

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return err
	}

	sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)

	// Create the volume.
	err = client.createVolume(vol.pool, volName, sizeBytes)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = client.deleteVolume(vol.pool, volName) })

	volumeFilesystem := vol.ConfigBlockFilesystem()
	if vol.contentType == ContentTypeFS {
		devPath, cleanup, err := d.getMappedDevPath(vol, true)
		if err != nil {
			return err
		}

		revert.Add(cleanup)

		_, err = makeFSType(devPath, volumeFilesystem, nil)
		if err != nil {
			return err
		}
	}

	// For VMs, also create the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(fsVol, op) })
	}

	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// Run the volume filler function if supplied.
		if filler != nil && filler.Fill != nil {
			var err error
			var devPath string

			if IsContentBlock(vol.contentType) {
				// Get the device path.
				devPath, err = d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}
			}

			allowUnsafeResize := false
			if vol.volType == VolumeTypeImage {
				// Allow filler to resize initial image volume as needed.
				// Some storage drivers don't normally allow image volumes to be resized due to
				// them having read-only snapshots that cannot be resized. However when creating
				// the initial image volume and filling it before the snapshot is taken resizing
				// can be allowed and is required in order to support unpacking images larger than
				// the default volume size. The filler function is still expected to obey any
				// volume size restrictions configured on the pool.
				// Unsafe resize is also needed to disable filesystem resize safety checks.
				// This is safe because if for some reason an error occurs the volume will be
				// discarded rather than leaving a corrupt filesystem.
				allowUnsafeResize = true
			}

			// Run the filler.
			err = d.runFiller(vol, devPath, filler, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Move the GPT alt header to end of disk if needed.
			if vol.IsVMBlock() {
				err = d.moveGPTAltHeader(devPath)
				if err != nil {
					return err
				}
			}
		}

		if vol.contentType == ContentTypeFS {
			// Run EnsureMountPath again after mounting and filling to ensure the mount directory has
			// the correct permissions set.
			err = vol.EnsureMountPath()
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

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *pure) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *pure) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	// Function to run once the volume is created, which will ensure appropriate permissions
	// on the mount path inside the volume, and resize the volume to specified size.
	postCreateTasks := func(v Volume) error {
		if vol.contentType == ContentTypeFS {
			// Mount the volume and ensure the permissions are set correctly inside the mounted volume.
			err := v.MountTask(func(_ string, _ *operations.Operation) error {
				return v.EnsureMountPath()
			}, op)
			if err != nil {
				return err
			}
		}

		// Resize volume to the size specified.
		err := d.SetVolumeQuota(v, vol.config["size"], false, op)
		if err != nil {
			return err
		}

		return nil
	}

	// For VMs, also copy the filesystem volume.
	if vol.IsVMBlock() {
		// Ensure that the volume's snapshots are also replaced with their filesystem counterpart.
		fsVolSnapshots := make([]Volume, 0, len(vol.Snapshots))
		for _, snapshot := range vol.Snapshots {
			fsVolSnapshots = append(fsVolSnapshots, snapshot.NewVMBlockFilesystemVolume())
		}

		srcFsVolSnapshots := make([]Volume, 0, len(srcVol.Snapshots))
		for _, snapshot := range srcVol.Snapshots {
			srcFsVolSnapshots = append(srcFsVolSnapshots, snapshot.NewVMBlockFilesystemVolume())
		}

		fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume(), fsVolSnapshots...)
		srcFSVol := NewVolumeCopy(srcVol.NewVMBlockFilesystemVolume(), srcFsVolSnapshots...)

		// Ensure parent UUID is retained for the filesystem volumes.
		fsVol.SetParentUUID(vol.parentUUID)
		srcFSVol.SetParentUUID(srcVol.parentUUID)

		err := d.CreateVolumeFromCopy(fsVol, srcFSVol, false, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(fsVol.Volume, op) })
	}

	poolName := vol.pool
	srcPoolName := srcVol.pool

	volName, err := d.getVolumeName(vol.Volume)
	if err != nil {
		return err
	}

	srcVolName, err := d.getVolumeName(srcVol.Volume)
	if err != nil {
		return err
	}

	// Since snapshots are first copied into destination volume from which a new snapshot is created,
	// we need to also remove the destination volume if an error occurs during copying of snapshots.
	deleteVolCopy := true

	// Copy volume snapshots.
	// Pure Storage does not allow copying snapshots along with the volume. Therefore, we
	// copy the snapshots sequentially. Each snapshot is first copied into destination
	// volume from which a new snapshot is created. The process is repeted until all
	// snapshots are copied.
	if !srcVol.IsSnapshot() {
		for _, snapshot := range vol.Snapshots {
			_, snapshotShortName, _ := api.GetParentAndSnapshotName(snapshot.name)

			// Find the corresponding source snapshot.
			var srcSnapshot *Volume
			for _, srcSnap := range srcVol.Snapshots {
				_, srcSnapshotShortName, _ := api.GetParentAndSnapshotName(srcSnap.name)
				if snapshotShortName == srcSnapshotShortName {
					srcSnapshot = &srcSnap
					break
				}
			}

			if srcSnapshot == nil {
				return fmt.Errorf("Failed to copy snapshot %q: Source snapshot does not exist", snapshotShortName)
			}

			srcSnapshotName, err := d.getVolumeName(*srcSnapshot)
			if err != nil {
				return err
			}

			// Copy the snapshot.
			err = d.client().copyVolumeSnapshot(srcPoolName, srcVolName, srcSnapshotName, poolName, volName)
			if err != nil {
				return fmt.Errorf("Failed copying snapshot %q: %w", snapshot.name, err)
			}

			if deleteVolCopy {
				// If at least one snapshot is copied into destination volume, we need to remove
				// that volume as well in case of an error.
				revert.Add(func() { _ = d.DeleteVolume(vol.Volume, op) })
				deleteVolCopy = false
			}

			// Set snapshot's parent UUID and retain source snapshot UUID.
			snapshot.SetParentUUID(vol.config["volatile.uuid"])

			// Create snapshot from a new volume (that was created from the source snapshot).
			// However, do not create VM's filesystem volume snapshot, as filesystem volume is
			// copied before block volume.
			err = d.createVolumeSnapshot(snapshot, false, op)
			if err != nil {
				return err
			}
		}
	}

	// Finally, copy the source volume (or snapshot) into destination volume snapshots.
	if srcVol.IsSnapshot() {
		// Get snapshot parent volume name.
		srcParentVol := srcVol.GetParent()
		srcParentVolName, err := d.getVolumeName(srcParentVol)
		if err != nil {
			return err
		}

		// Copy the source snapshot into destination volume.
		err = d.client().copyVolumeSnapshot(srcPoolName, srcParentVolName, srcVolName, poolName, volName)
		if err != nil {
			return err
		}
	} else {
		err = d.client().copyVolume(srcPoolName, srcVolName, poolName, volName, true)
		if err != nil {
			return err
		}
	}

	// Add reverted to delete destination volume, if not already added.
	if deleteVolCopy {
		revert.Add(func() { _ = d.DeleteVolume(vol.Volume, op) })
	}

	err = postCreateTasks(vol.Volume)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *pure) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// When performing a cluster member move prepare the volumes on the target side.
	if volTargetArgs.ClusterMoveSourceName != "" {
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		if vol.IsVMBlock() {
			fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume())
			err := d.CreateVolumeFromMigration(fsVol, conn, volTargetArgs, preFiller, op)
			if err != nil {
				return err
			}
		}

		return nil
	}

	_, err := genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	return err
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *pure) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	// For VMs, also copy the filesystem volume.
	if vol.IsVMBlock() {
		// Ensure that the volume's snapshots are also replaced with their filesystem counterpart.
		fsVolSnapshots := make([]Volume, 0, len(vol.Snapshots))
		for _, snapshot := range vol.Snapshots {
			fsVolSnapshots = append(fsVolSnapshots, snapshot.NewVMBlockFilesystemVolume())
		}

		srcFsVolSnapshots := make([]Volume, 0, len(srcVol.Snapshots))
		for _, snapshot := range srcVol.Snapshots {
			srcFsVolSnapshots = append(srcFsVolSnapshots, snapshot.NewVMBlockFilesystemVolume())
		}

		fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume(), fsVolSnapshots...)
		srcFSVol := NewVolumeCopy(srcVol.NewVMBlockFilesystemVolume(), srcFsVolSnapshots...)

		cleanup, err := d.refreshVolume(fsVol, srcFSVol, refreshSnapshots, allowInconsistent, op)
		if err != nil {
			return err
		}

		revert.Add(cleanup)
	}

	cleanup, err := d.refreshVolume(vol, srcVol, refreshSnapshots, allowInconsistent, op)
	if err != nil {
		return err
	}

	revert.Add(cleanup)

	revert.Success()
	return nil
}

// refreshVolume updates an existing volume to match the state of another. For VMs, this function
// refreshes either block or filesystem volume, depending on the volume type. Therefore, the caller
// needs to ensure it is called twice - once for each volume type.
func (d *pure) refreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Function to run once the volume is created, which will ensure appropriate permissions
	// on the mount path inside the volume, and resize the volume to specified size.
	postCreateTasks := func(v Volume) error {
		if vol.contentType == ContentTypeFS {
			// Mount the volume and ensure the permissions are set correctly inside the mounted volume.
			err := v.MountTask(func(_ string, _ *operations.Operation) error {
				return v.EnsureMountPath()
			}, op)
			if err != nil {
				return err
			}
		}

		// Resize volume to the size specified.
		err := d.SetVolumeQuota(vol.Volume, vol.ConfigSize(), false, op)
		if err != nil {
			return err
		}

		return nil
	}

	srcPoolName := srcVol.pool
	poolName := vol.pool

	srcVolName, err := d.getVolumeName(srcVol.Volume)
	if err != nil {
		return nil, err
	}

	volName, err := d.getVolumeName(vol.Volume)
	if err != nil {
		return nil, err
	}

	// Create new reverter snapshot, which is used to revert the original volume in case of
	// an error. Snapshots are also required to be first copied into destination volume,
	// from which a new snapshot is created to effectively copy a snapshot. If any error
	// occurs, the destination volume has been already modified and needs reverting.
	reverterSnapshotName := "lxd-reverter-snapshot"

	// Remove existing reverter snapshot.
	err = d.client().deleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	}

	// Create new reverter snapshot.
	err = d.client().createVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		// Restore destination volume from reverter snapshot and remove the snapshot afterwards.
		_ = d.client().restoreVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
		_ = d.client().deleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
	})

	if !srcVol.IsSnapshot() && len(refreshSnapshots) > 0 {
		var refreshedSnapshots []string

		// Refresh volume snapshots.
		// Pure Storage does not allow copying snapshots along with the volume. Therefore,
		// we copy the missing snapshots sequentially. Each snapshot is first copied into
		// destination volume from which a new snapshot is created. The process is repeted
		// until all of the missing snapshots are copied.
		for _, snapshot := range vol.Snapshots {
			// Remove volume name prefix from the snapshot name, and check whether it
			// has to be refreshed.
			_, snapshotShortName, _ := api.GetParentAndSnapshotName(snapshot.name)
			if !slices.Contains(refreshSnapshots, snapshotShortName) {
				// Skip snapshot if it doesn't have to be refreshed.
				continue
			}

			// Find the corresponding source snapshot.
			var srcSnapshot *Volume
			for _, srcSnap := range srcVol.Snapshots {
				_, srcSnapshotShortName, _ := api.GetParentAndSnapshotName(srcSnap.name)
				if snapshotShortName == srcSnapshotShortName {
					srcSnapshot = &srcSnap
					break
				}
			}

			if srcSnapshot == nil {
				return nil, fmt.Errorf("Failed to refresh snapshot %q: Source snapshot does not exist", snapshotShortName)
			}

			srcSnapshotName, err := d.getVolumeName(*srcSnapshot)
			if err != nil {
				return nil, err
			}

			// Overwrite existing destination volume with snapshot.
			err = d.client().copyVolumeSnapshot(srcPoolName, srcVolName, srcSnapshotName, poolName, volName)
			if err != nil {
				return nil, err
			}

			// Set snapshot's parent UUID.
			snapshot.SetParentUUID(vol.config["volatile.uuid"])

			// Create snapshot of a new volume. Do not copy VM's filesystem volume snapshot,
			// as FS volumes are already copied by this point.
			err = d.createVolumeSnapshot(snapshot, false, op)
			if err != nil {
				return nil, err
			}

			revert.Add(func() { _ = d.DeleteVolumeSnapshot(snapshot, op) })

			// Append snapshot to the list of successfully refreshed snapshots.
			refreshedSnapshots = append(refreshedSnapshots, snapshotShortName)
		}

		// Ensure all snapshots were successfully refreshed.
		missing := shared.RemoveElementsFromSlice(refreshSnapshots, refreshedSnapshots...)
		if len(missing) > 0 {
			return nil, fmt.Errorf("Failed to refresh snapshots %v", missing)
		}
	}

	// Finally, copy the source volume (or snapshot) into destination volume snapshots.
	if srcVol.IsSnapshot() {
		// Find snapshot parent volume.
		srcParentVol := srcVol.GetParent()
		srcParentVolName, err := d.getVolumeName(srcParentVol)
		if err != nil {
			return nil, err
		}

		// Copy the source snapshot into destination volume.
		err = d.client().copyVolumeSnapshot(srcPoolName, srcParentVolName, srcVolName, poolName, volName)
		if err != nil {
			return nil, err
		}
	} else {
		err = d.client().copyVolume(srcPoolName, srcVolName, poolName, volName, true)
		if err != nil {
			return nil, err
		}
	}

	err = postCreateTasks(vol.Volume)
	if err != nil {
		return nil, err
	}

	cleanup := revert.Clone().Fail
	revert.Success()

	// Remove temporary reverter snapshot.
	_ = d.client().deleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)

	return cleanup, err
}

// DeleteVolume deletes the volume and all associated snapshots.
func (d *pure) DeleteVolume(vol Volume, op *operations.Operation) error {
	volExists, err := d.HasVolume(vol)
	if err != nil {
		return err
	}

	if !volExists {
		return nil
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	host, err := d.client().getCurrentHost()
	if err != nil {
		// If the host doesn't exist, continue with the deletion of
		// the volume and do not try to delete the volume mapping as
		// it cannot exist.
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	} else {
		// Delete the volume mapping with the host.
		err = d.client().disconnectHostFromVolume(vol.pool, volName, host.Name)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	err = d.client().deleteVolume(vol.pool, volName)
	if err != nil {
		return err
	}

	// For VMs, also delete the filesystem volume.
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
			return fmt.Errorf("Failed to remove %q: %w", mountPath, err)
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *pure) HasVolume(vol Volume) (bool, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return false, err
	}

	// If volume represents a snapshot, also retrieve (encoded) volume name of the parent,
	// and check if the snapshot exists.
	if vol.IsSnapshot() {
		parentVol := vol.GetParent()
		parentVolName, err := d.getVolumeName(parentVol)
		if err != nil {
			return false, err
		}

		_, err = d.client().getVolumeSnapshot(vol.pool, parentVolName, volName)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return false, nil
			}

			return false, err
		}

		return true, nil
	}

	// Otherwise, check if the volume exists.
	_, err = d.client().getVolume(vol.pool, volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// FillVolumeConfig populate volume with default config.
func (d *pure) FillVolumeConfig(vol Volume) error {
	// Copy volume.* configuration options from pool.
	// Exclude 'block.filesystem' and 'block.mount_options'
	// as these ones are handled below in this function and depend on the volume's type.
	err := d.fillVolumeConfig(&vol, "block.filesystem", "block.mount_options")
	if err != nil {
		return err
	}

	// Only validate filesystem config keys for filesystem volumes or VM block volumes (which have an
	// associated filesystem volume).
	if vol.ContentType() == ContentTypeFS || vol.IsVMBlock() {
		// VM volumes will always use the default filesystem.
		if vol.IsVMBlock() {
			vol.config["block.filesystem"] = DefaultFilesystem
		} else {
			// Inherit filesystem from pool if not set.
			if vol.config["block.filesystem"] == "" {
				vol.config["block.filesystem"] = d.config["volume.block.filesystem"]
			}

			// Default filesystem if neither volume nor pool specify an override.
			if vol.config["block.filesystem"] == "" {
				// Unchangeable volume property: Set unconditionally.
				vol.config["block.filesystem"] = DefaultFilesystem
			}
		}

		// Inherit filesystem mount options from pool if not set.
		if vol.config["block.mount_options"] == "" {
			vol.config["block.mount_options"] = d.config["volume.block.mount_options"]
		}

		// Default filesystem mount options if neither volume nor pool specify an override.
		if vol.config["block.mount_options"] == "" {
			// Unchangeable volume property: Set unconditionally.
			vol.config["block.mount_options"] = "discard"
		}
	}

	return nil
}

// ValidateVolume validates the supplied volume config.
func (d *pure) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	// When creating volumes from ISO images, round its size to the next multiple of 512B,
	// and ensure it is at least 1MiB.
	if vol.ContentType() == ContentTypeISO {
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)
		vol.SetConfigSize(strconv.FormatInt(sizeBytes, 10))
	}

	commonRules := d.commonVolumeRules()

	// Disallow block.* settings for regular custom block volumes. These settings only make sense
	// when using custom filesystem volumes. LXD will create the filesystem for these volumes,
	// and use the mount options. When attaching a regular block volume to a VM, these are not
	// mounted by LXD and therefore don't need these config keys.
	if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	}

	return d.validateVolume(vol, commonRules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *pure) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *pure) GetVolumeUsage(vol Volume) (int64, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return -1, err
	}

	pureVol, err := d.client().getVolume(vol.pool, volName)
	if err != nil {
		return -1, err
	}

	return pureVol.Space.UsedBytes, nil
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an non-positive size.
func (d *pure) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	// Get volume and retrieve current size.
	pureVol, err := d.client().getVolume(vol.pool, volName)
	if err != nil {
		return err
	}

	oldSizeBytes := pureVol.Space.TotalBytes

	// Do nothing if volume is already specified size (+/- 512 bytes).
	if oldSizeBytes+512 > sizeBytes && oldSizeBytes-512 < sizeBytes {
		return nil
	}

	inUse := vol.MountInUse()
	truncate := sizeBytes < oldSizeBytes

	// Resize filesystem if needed.
	if vol.contentType == ContentTypeFS {
		fsType := vol.ConfigBlockFilesystem()

		if sizeBytes < oldSizeBytes {
			if !filesystemTypeCanBeShrunk(fsType) {
				return fmt.Errorf("Filesystem %q cannot be shrunk: %w", fsType, ErrCannotBeShrunk)
			}

			if inUse {
				// We don't allow online shrinking of filesytem volumes.
				// Returning this error ensures the disk is resized next
				// time the instance is started.
				return ErrInUse
			}

			devPath, cleanup, err := d.getMappedDevPath(vol, true)
			if err != nil {
				return err
			}

			defer cleanup()

			// Shrink filesystem first.
			err = shrinkFileSystem(fsType, devPath, vol, sizeBytes, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Shrink the block device.
			err = d.client().resizeVolume(vol.pool, volName, sizeBytes, truncate)
			if err != nil {
				return err
			}

			err = block.RefreshDiskDeviceSize(d.state.ShutdownCtx, devPath)
			if err != nil {
				return fmt.Errorf("Failed refreshing volume %q size: %w", vol.name, err)
			}

			err = block.WaitDiskDeviceResize(d.state.ShutdownCtx, devPath, sizeBytes)
			if err != nil {
				return err
			}
		} else {
			// Grow block device first.
			err = d.client().resizeVolume(vol.pool, volName, sizeBytes, truncate)
			if err != nil {
				return err
			}

			devPath, cleanup, err := d.getMappedDevPath(vol, true)
			if err != nil {
				return err
			}

			defer cleanup()

			err = block.RefreshDiskDeviceSize(d.state.ShutdownCtx, devPath)
			if err != nil {
				return fmt.Errorf("Failed refreshing volume %q size: %w", vol.name, err)
			}

			// Ensure the block device is resized before growing the filesystem.
			// This should succeed immediately, but if volume was already mapped,
			// it may take a moment for the size to be reflected on the host.
			err = block.WaitDiskDeviceResize(d.state.ShutdownCtx, devPath, sizeBytes)
			if err != nil {
				return err
			}

			// Grow the filesystem to fill the block device.
			err = growFileSystem(fsType, devPath, vol)
			if err != nil {
				return err
			}
		}
	} else {
		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize {
			if sizeBytes < oldSizeBytes {
				return fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
			}

			if inUse {
				// We don't allow online shrinking of filesytem volumes.
				// Returning this error ensures the disk is resized next
				// time the instance is started.
				return ErrInUse
			}
		}

		// Resize block device.
		err = d.client().resizeVolume(vol.pool, volName, sizeBytes, truncate)
		if err != nil {
			return err
		}

		devPath, cleanup, err := d.getMappedDevPath(vol, true)
		if err != nil {
			return err
		}

		defer cleanup()

		err = block.RefreshDiskDeviceSize(d.state.ShutdownCtx, devPath)
		if err != nil {
			return fmt.Errorf("Failed refreshing volume %q size: %w", vol.name, err)
		}

		// Wait for the block device to be resized before moving GPT alt header.
		// This ensures that the GPT alt header is not moved before the actual
		// size is reflected on a local host. Otherwise, the GPT alt header
		// would be moved to the same location.
		err = block.WaitDiskDeviceResize(d.state.ShutdownCtx, devPath, sizeBytes)
		if err != nil {
			return err
		}

		// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as it is
		// expected the caller will do all necessary post resize actions themselves).
		if vol.IsVMBlock() && !allowUnsafeResize {
			err = d.moveGPTAltHeader(devPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *pure) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevPath(vol, false)
		return devPath, err
	}

	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *pure) ListVolumes() ([]Volume, error) {
	return []Volume{}, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *pure) MountVolume(vol Volume, op *operations.Operation) error {
	return mountVolume(d, vol, d.getMappedDevPath, op)
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
func (d *pure) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return unmountVolume(d, vol, keepBlockDev, d.getMappedDevPath, d.unmapVolume, op)
}

// RenameVolume renames a volume and its snapshots.
func (d *pure) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume won't change an actual name of the Pure Storage volume.
	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *pure) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
	ourUnmount, err := d.UnmountVolume(vol, false, op)
	if err != nil {
		return err
	}

	if ourUnmount {
		defer func() { _ = d.MountVolume(vol, op) }()
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	// Overwrite existing volume by copying the given snapshot content into it.
	err = d.client().restoreVolumeSnapshot(vol.pool, volName, snapVolName)
	if err != nil {
		return err
	}

	// For VMs, also restore the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		snapFSVol := snapVol.NewVMBlockFilesystemVolume()
		snapFSVol.SetParentUUID(snapVol.parentUUID)

		err := d.RestoreVolume(fsVol, snapFSVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MigrateVolume sends a volume for migration.
func (d *pure) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	// When performing a cluster member move don't do anything on the source member.
	if volSrcArgs.ClusterMove {
		return nil
	}

	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume creates an exported version of a volume.
func (d *pure) BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *pure) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return d.createVolumeSnapshot(snapVol, true, op)
}

// createVolumeSnapshot creates a snapshot of a volume. If snapshotVMfilesystem is false, a VM's filesystem volume
// is not copied.
func (d *pure) createVolumeSnapshot(snapVol Volume, snapshotVMfilesystem bool, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)

	if filesystem.IsMountPoint(sourcePath) {
		// Attempt to sync and freeze filesystem, but do not error if not able to freeze (as filesystem
		// could still be busy), as we do not guarantee the consistency of a snapshot. This is costly but
		// try to ensure that all cached data has been committed to disk. If we don't then the snapshot
		// of the underlying filesystem can be inconsistent or, in the worst case, empty.
		unfreezeFS, err := d.filesystemFreeze(sourcePath)
		if err == nil {
			defer func() { _ = unfreezeFS() }()
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

	parentVol := snapVol.GetParent()
	parentVolName, err := d.getVolumeName(parentVol)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	err = d.client().createVolumeSnapshot(snapVol.pool, parentVolName, snapVolName)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = d.DeleteVolumeSnapshot(snapVol, op) })

	// For VMs, create a snapshot of the filesystem volume too.
	// Skip if snapshotVMfilesystem is false to prevent overwriting separately copied volumes.
	if snapVol.IsVMBlock() && snapshotVMfilesystem {
		fsVol := snapVol.NewVMBlockFilesystemVolume()

		// Set the parent volume's UUID.
		fsVol.SetParentUUID(snapVol.parentUUID)

		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolumeSnapshot(fsVol, op) })
	}

	revert.Success()
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *pure) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentVol := snapVol.GetParent()
	parentVolName, err := d.getVolumeName(parentVol)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	// Snapshots cannot be mounted directly, so when this is needed, the snapshot is copied
	// into a temporary volume. In case LXD was abruptly stopped in the moment when
	// temporary volume was created, it is possible that the volume was not removed.
	tmpVol, err := d.client().getVolume(snapVol.pool, snapVolName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed retrieving temporary snapshot volume: %w", err)
	}

	if tmpVol != nil {
		// Temporary volume found, delete it.
		err = d.client().deleteVolume(snapVol.pool, snapVolName)
		if err != nil {
			return fmt.Errorf("Failed deleting temporary snapshot volume: %w", err)
		}
	}

	// Delete snapshot.
	err = d.client().deleteVolumeSnapshot(snapVol.pool, parentVolName, snapVolName)
	if err != nil {
		return err
	}

	mountPath := snapVol.MountPath()

	if snapVol.contentType == ContentTypeFS && shared.PathExists(mountPath) {
		err = wipeDirectory(mountPath)
		if err != nil {
			return err
		}

		err = os.Remove(mountPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed to remove %q: %w", mountPath, err)
		}
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentVol.name)
	if err != nil {
		return err
	}

	// For VM images, delete the filesystem volume too.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		fsVol.SetParentUUID(snapVol.parentUUID)

		err := d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountVolumeSnapshot creates a new temporary volume from a volume snapshot to allow mounting it.
func (d *pure) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	parentVol := snapVol.GetParent()

	// Get the parent volume name.
	parentVolName, err := d.getVolumeName(parentVol)
	if err != nil {
		return err
	}

	// Get the snapshot volume name.
	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	// A Pure Storage snapshot cannot be mounted. To mount a snapshot, a new volume
	// has to be created from the snapshot.
	err = d.client().copyVolumeSnapshot(snapVol.pool, parentVolName, snapVolName, snapVol.pool, snapVolName)
	if err != nil {
		return err
	}

	// Ensure temporary snapshot volume is removed in case of an error.
	revert.Add(func() { _ = d.client().deleteVolume(snapVol.pool, snapVolName) })

	// For VMs, also create the temporary filesystem volume snapshot.
	if snapVol.IsVMBlock() {
		snapFsVol := snapVol.NewVMBlockFilesystemVolume()
		snapFsVol.SetParentUUID(snapVol.parentUUID)

		parentFsVol := snapFsVol.GetParent()

		snapFsVolName, err := d.getVolumeName(snapFsVol)
		if err != nil {
			return err
		}

		parentFsVolName, err := d.getVolumeName(parentFsVol)
		if err != nil {
			return err
		}

		err = d.client().copyVolumeSnapshot(snapVol.pool, parentFsVolName, snapFsVolName, snapVol.pool, snapFsVolName)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.client().deleteVolume(snapVol.pool, snapFsVolName) })
	}

	err = d.MountVolume(snapVol, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// UnmountVolumeSnapshot unmountes and deletes volume that was temporary created from a snapshot
// to allow mounting it.
func (d *pure) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	ourUnmount, err := d.UnmountVolume(snapVol, false, op)
	if err != nil {
		return false, err
	}

	if !ourUnmount {
		return false, nil
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return true, err
	}

	// Cleanup temporary snapshot volume.
	err = d.client().deleteVolume(snapVol.pool, snapVolName)
	if err != nil {
		return true, err
	}

	// For VMs, also cleanup the temporary volume for a filesystem snapshot.
	if snapVol.IsVMBlock() {
		snapFsVol := snapVol.NewVMBlockFilesystemVolume()
		snapFsVolName, err := d.getVolumeName(snapFsVol)
		if err != nil {
			return true, err
		}

		err = d.client().deleteVolume(snapVol.pool, snapFsVolName)
		if err != nil {
			return true, err
		}
	}

	return ourUnmount, nil
}

// VolumeSnapshots returns a list of Pure Storage snapshot names for the given volume (in no particular order).
func (d *pure) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	volumeSnapshots, err := d.client().getVolumeSnapshots(vol.pool, volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, nil
		}

		return nil, err
	}

	snapshotNames := make([]string, 0, len(volumeSnapshots))
	for _, snapshot := range volumeSnapshots {
		// Snapshot name contains storage pool and volume names as prefix.
		// Storage pool is delimited with double colon (::) and volume with a dot.
		_, volAndSnapName, _ := strings.Cut(snapshot.Name, "::")
		_, snapshotName, _ := strings.Cut(volAndSnapName, ".")

		snapshotNames = append(snapshotNames, snapshotName)
	}

	return snapshotNames, nil
}

// CheckVolumeSnapshots checks that the volume's snapshots, according to the storage driver,
// match those provided. Note that additional snapshots may exist within the Pure Storage pool
// if protection groups are configured outside of LXD.
func (d *pure) CheckVolumeSnapshots(vol Volume, snapVols []Volume, op *operations.Operation) error {
	// Get all of the volume's snapshots in base64 encoded format.
	storageSnapshotNames, err := vol.driver.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	// Check if the provided list of volume snapshots matches the ones from the storage.
	for _, snap := range snapVols {
		snapName, err := d.getVolumeName(snap)
		if err != nil {
			return err
		}

		if !slices.Contains(storageSnapshotNames, snapName) {
			return fmt.Errorf("Snapshot %q expected but not in storage", snapName)
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *pure) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	// Renaming a volume snapshot won't change an actual name of the Pure Storage volume snapshot.
	return nil
}
