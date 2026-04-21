package drivers

import (
	"io"
	"strconv"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *powerstore) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		//  scope: global
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		//  scope: global
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=size)
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TiB.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"size": validate.Optional(validate.IsMultipleOfUnit("1MiB")),
	}
}

// FillVolumeConfig populate volume with default config.
func (d *powerstore) FillVolumeConfig(vol Volume) error {
	// Copy volume.* configuration options from pool.
	// Exclude 'block.filesystem' and 'block.mount_options'
	// as these ones are handled below in this function and depend on the volume's type.
	err := d.fillVolumeConfig(&vol, "block.filesystem", "block.mount_options")
	if err != nil {
		return err
	}

	// Only validate filesystem config keys for filesystem volumes or VM block
	// volumes (which have an associated filesystem volume).
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
func (d *powerstore) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	if vol.ContentType() == ContentTypeISO {
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)
		vol.SetConfigSize(strconv.FormatInt(sizeBytes, 10))
	}

	commonRules := d.commonVolumeRules()

	// Disallow block.* settings for regular custom block volumes. These settings only make
	// sense when using custom filesystem volumes. LXD will create the filesystem for these
	// volumes, and use the mount options. When attaching a regular block volume to a VM,
	// these are not mounted by LXD and therefore don't need these config keys.
	if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	}

	return d.validateVolume(vol, commonRules, removeUnknownKeys)
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *powerstore) GetVolumeDiskPath(vol Volume) (string, error) {
	return "", ErrNotSupported
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *powerstore) GetVolumeUsage(vol Volume) (int64, error) {
	return -1, ErrNotSupported
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *powerstore) HasVolume(vol Volume) (bool, error) {
	return false, nil
}

// ListVolumes returns a list of LXD volumes in storage pool.
// It returns all volumes and sets the volume's volatile.uuid extracted from the name.
func (d *powerstore) ListVolumes() ([]Volume, error) {
	return nil, ErrNotSupported
}

// CreateVolume creates an empty volume and can optionally fill it by executing
// the supplied filler function.
func (d *powerstore) CreateVolume(vol Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *powerstore) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) (VolumePostHook, revert.Hook, error) {
	return nil, nil, ErrNotSupported
}

// CreateVolumeFromImage creates a new volume from an image, unpacking it directly.
func (d *powerstore) CreateVolumeFromImage(vol Volume, imgVol *Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *powerstore) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *powerstore) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// UpdateVolume applies config changes to the volume.
func (d *powerstore) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	return ErrNotSupported
}

// DeleteVolume deletes a volume of the storage device.
func (d *powerstore) DeleteVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// RenameVolume is a no-op as renaming a volume does not change the name of the associated volume
// resource on the remote storage.
func (d *powerstore) RenameVolume(vol Volume, newVolName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// BackupVolume creates an exported version of a volume.
func (d *powerstore) BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// MigrateVolume sends a volume for migration.
func (d *powerstore) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// RestoreVolume restores a volume from a snapshot.
func (d *powerstore) RestoreVolume(vol Volume, snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *powerstore) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
func (d *powerstore) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// MountVolume mounts a volume and increments ref counter.
func (d *powerstore) MountVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// UnmountVolume simulates unmounting a volume.
//
// keepBlockDev indicates whether the backing block device should be kept mapped to the
// host if the volume is unmounted.
func (d *powerstore) UnmountVolume(vol Volume, keepBlockDev bool, progressReporter ioprogress.ProgressReporter) (bool, error) {
	return false, ErrNotSupported
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *powerstore) CreateVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *powerstore) DeleteVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// RenameVolumeSnapshot is a no-op as renaming a volume snapshot does not change the name of
// the associated volume resource on the remote storage.
func (d *powerstore) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// VolumeSnapshots returns a list of volume snapshot names for the given volume.
func (d *powerstore) VolumeSnapshots(vol Volume) ([]string, error) {
	return nil, ErrNotSupported
}

// MountVolumeSnapshot mounts a volume snapshot.
func (d *powerstore) MountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	return ErrNotSupported
}

// UnmountVolumeSnapshot unmounts the volume snapshot.
func (d *powerstore) UnmountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) (bool, error) {
	return false, ErrNotSupported
}
