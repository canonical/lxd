package drivers

import (
	"fmt"
	"io"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
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
		"size": validate.Optional(validate.IsMultipleOfUnit("512B")),
	}
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *pure) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *pure) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *pure) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *pure) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	_, err := genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	return err
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *pure) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
	return err
}

// DeleteVolume deletes a volume of the storage device.
// If any snapshots of the volume remain then this function will return an error.
func (d *pure) DeleteVolume(vol Volume, op *operations.Operation) error {
	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *pure) HasVolume(vol Volume) (bool, error) {
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
	// When creating volumes from ISO images, round its size to the next multiple of 512B.
	if vol.ContentType() == ContentTypeISO {
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		// If the remainder when dividing by 512 is greater than 0, round the size up
		// to the next multiple of 512.
		remainder := sizeBytes % 512
		if remainder > 0 {
			sizeBytes = (sizeBytes/512 + 1) * 512
			vol.SetConfigSize(fmt.Sprintf("%d", sizeBytes))
		}
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
	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *pure) GetVolumeUsage(vol Volume) (int64, error) {
	return 0, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size.
func (d *pure) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return nil
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *pure) GetVolumeDiskPath(vol Volume) (string, error) {
	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *pure) ListVolumes() ([]Volume, error) {
	return []Volume{}, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *pure) MountVolume(vol Volume, op *operations.Operation) error {
	return nil
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
func (d *pure) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return false, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *pure) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume won't change an actual name of the Pure Storage volume.
	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *pure) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
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
func (d *pure) BackupVolume(vol VolumeCopy, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *pure) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *pure) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return nil
}

// MountVolumeSnapshot simulates mounting a volume snapshot.
func (d *pure) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return d.MountVolume(snapVol, op)
}

// UnmountVolumeSnapshot simulates unmounting a volume snapshot.
func (d *pure) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	return d.UnmountVolume(snapVol, false, op)
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *pure) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return []string{}, nil
}

// CheckVolumeSnapshots checks that the volume's snapshots, according to the storage driver, match those provided.
func (d *pure) CheckVolumeSnapshots(vol Volume, snapVols []Volume, op *operations.Operation) error {
	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *pure) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	// Renaming a volume snapshot won't change an actual name of the Pure Storage volume snapshot.
	return nil
}
