package drivers

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/backup"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// factorGiB divides a byte size value into Gibibytes.
const factorGiB = 1024 * 1024 * 1024

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *powerflex) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	// Get raw the volume size in GiB.
	// PowerFlex accepts values without unit only.
	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return err
	}

	sizeGiB := sizeBytes / factorGiB

	client := d.client()
	pool, err := d.resolvePool()
	if err != nil {
		return err
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	id, err := client.createVolume(volName, sizeGiB, d.getVolumeType(vol), pool.ID)
	if err != nil {
		return err
	}

	// In case of error delete the volume with mode "ONLY_ME".
	// This removes the given volume only without any parent(s) or child(s).
	revert.Add(func() { _ = client.deleteVolume(id, "ONLY_ME") })

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
func (d *powerflex) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *powerflex) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	// Function to run once the volume is created, which will ensure
	// permissions on mount path inside the volume are correct, and resize the volume to specified size.
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

	// Copy without snapshots.
	// If the pools config doesn't enforce creating clone copies of the volume, snapshot the volume
	// in PowerFlex to create a new standalone volume.
	// If the source volume is of type image, lazy copying is enforced which prevents using optimized image storage
	// but effectively allows to circumvent the PowerFlex limit of 126 snapshots.
	client := d.client()
	if len(vol.Snapshots) == 0 && shared.IsFalse(d.config["powerflex.clone_copy"]) {
		pool, err := d.resolvePool()
		if err != nil {
			return err
		}

		domain, err := client.getProtectionDomain(pool.ProtectionDomainID)
		if err != nil {
			return err
		}

		srcVolName, err := d.getVolumeName(srcVol.Volume)
		if err != nil {
			return err
		}

		volumeID, err := client.getVolumeID(srcVolName)
		if err != nil {
			return err
		}

		volName, err := d.getVolumeName(vol.Volume)
		if err != nil {
			return err
		}

		_, err = client.createVolumeSnapshot(domain.SystemID, volumeID, volName, "ReadWrite")
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(vol.Volume, op) })

		// For VMs, also copy the filesystem volume.
		if vol.IsVMBlock() {
			srcFSVol := NewVolumeCopy(srcVol.NewVMBlockFilesystemVolume())
			fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume())
			err := d.CreateVolumeFromCopy(fsVol, srcFSVol, false, op)
			if err != nil {
				return err
			}
		}

		err = postCreateTasks(vol.Volume)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	var srcVolumeSnapshots []string
	for _, snapshot := range vol.Snapshots {
		_, snapshotName, _ := api.GetParentAndSnapshotName(snapshot.name)
		srcVolumeSnapshots = append(srcVolumeSnapshots, snapshotName)
	}

	// Copy "lazy" with snapshots.
	// If clone copies are enforced by the pools config or the volume has snapshots that need to be copied,
	// fallback to simply copying the contents between source and target volumes.
	cleanup, err := genericVFSCopyVolume(d, nil, vol, srcVol, srcVolumeSnapshots, false, allowInconsistent, op)
	if err != nil {
		return err
	}

	revert.Add(cleanup)

	revert.Success()
	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *powerflex) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
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
func (d *powerflex) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
	return err
}

// DeleteVolume deletes a volume of the storage device.
// If any snapshots of the volume remain then this function will return an error.
func (d *powerflex) DeleteVolume(vol Volume, op *operations.Operation) error {
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

	client := d.client()
	id, err := client.getVolumeID(volName)
	if err != nil {
		return err
	}

	volume, err := client.getVolume(id)
	if err != nil {
		return err
	}

	for _, mapping := range volume.MappedSDCInfo {
		err := client.deleteHostVolumeMapping(mapping.SDCID, id)
		if err != nil {
			return err
		}
	}

	err = client.deleteVolume(id, "ONLY_ME")
	if err != nil {
		return err
	}

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
			return fmt.Errorf("Failed to remove '%s': %w", mountPath, err)
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *powerflex) HasVolume(vol Volume) (bool, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return false, err
	}

	_, err = d.client().getVolumeID(volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// FillVolumeConfig populate volume with default config.
func (d *powerflex) FillVolumeConfig(vol Volume) error {
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

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *powerflex) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerflex; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-powerflex; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=volume-conf; key=block.type)
		//
		// ---
		//  type: string
		//  defaultdesc: same as `volume.block.type` or `thick`
		//  shortdesc: Whether to create a `thin` or `thick` provisioned volume
		"block.type": validate.Optional(validate.IsOneOf("thin", "thick")),
		// lxdmeta:generate(entities=storage-powerflex; group=volume-conf; key=size)
		// The size must be in multiples of 8 GiB.
		// See {ref}`storage-powerflex-limitations` for more information.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		"size": validate.Optional(validate.IsMultipleOfUnit("8GiB")),
	}
}

// ValidateVolume validates the supplied volume config.
func (d *powerflex) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	// When creating volumes from ISO images, round its size to the next multiple of 8GiB.
	if vol.ContentType() == ContentTypeISO {
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		// Get the volumes size in GiB.
		// Always round to the next full GiB.
		sizeGiB := int64(math.Ceil(float64(sizeBytes) / float64(factorGiB)))

		// Get the rest of the modulo operation.
		nonMultipleRest := sizeGiB % 8

		// Check how many times the given size can be divided by 8.
		multipleCount := sizeGiB / 8

		// If the given size is smaller than 8, create a volume with at least 8GiB.
		if nonMultipleRest != 0 {
			multipleCount++
		}

		vol.SetConfigSize(fmt.Sprintf("%d", multipleCount*factorGiB*8))
	}

	commonRules := d.commonVolumeRules()

	// Disallow block.* settings for regular custom block volumes. These settings only make sense
	// when using custom filesystem volumes. LXD will create the filesystem
	// for these volumes, and use the mount options. When attaching a regular block volume to a VM,
	// these are not mounted by LXD and therefore don't need these config keys.
	if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	}

	return d.validateVolume(vol, commonRules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *powerflex) UpdateVolume(vol Volume, changedConfig map[string]string) error {
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
func (d *powerflex) GetVolumeUsage(vol Volume) (int64, error) {
	// If mounted, use the filesystem stats for pretty accurate usage information.
	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(vol.MountPath()) {
		var stat unix.Statfs_t

		err := unix.Statfs(vol.MountPath(), &stat)
		if err != nil {
			return -1, err
		}

		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	}

	// Getting the usage of an unmounted volume is not supported.
	// PowerFlex reports the usage on pool level only.
	return 0, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size.
func (d *powerflex) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	devPath, cleanup, err := d.getMappedDevPath(vol, true)
	if err != nil {
		return err
	}

	defer cleanup()

	oldSizeBytes, err := block.DiskSizeBytes(devPath)
	if err != nil {
		return fmt.Errorf("Error getting current size: %w", err)
	}

	// Do nothing if volume is already specified size (+/- 512 bytes).
	if oldSizeBytes+512 > sizeBytes && oldSizeBytes-512 < sizeBytes {
		return nil
	}

	// PowerFlex supports increasing of size only.
	if sizeBytes < oldSizeBytes {
		return fmt.Errorf("Volume capacity can only be increased")
	}

	// Block image volumes cannot be resized because they have a readonly snapshot that doesn't get
	// updated when the volume's size is changed, and this is what instances are created from.
	// During initial volume fill allowUnsafeResize is enabled because snapshot hasn't been taken yet.
	if !allowUnsafeResize && vol.volType == VolumeTypeImage {
		return ErrNotSupported
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	client := d.client()
	volumeID, err := client.getVolumeID(volName)
	if err != nil {
		return err
	}

	// Resize filesystem if needed.
	if vol.contentType == ContentTypeFS {
		fsType := vol.ConfigBlockFilesystem()

		if sizeBytes > oldSizeBytes {
			// Grow block device first.
			err = client.setVolumeSize(volumeID, sizeBytes/factorGiB)
			if err != nil {
				return err
			}

			// Grow the filesystem to fill block device.
			err = growFileSystem(fsType, devPath, vol)
			if err != nil {
				return err
			}
		}
	} else {
		inUse := vol.MountInUse()

		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize && inUse {
			// We don't allow online resizing of block volumes.
			return ErrInUse
		}

		// Resize block device.
		err = client.setVolumeSize(volumeID, sizeBytes/factorGiB)
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
func (d *powerflex) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevPath(vol, false)
		return devPath, err
	}

	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
// TODO: Add support for recovering volumes and deleting left over volumes in case of pool deletion.
func (d *powerflex) ListVolumes() ([]Volume, error) {
	return []Volume{}, nil
}

// DefaultVMBlockFilesystemSize returns the size of a VM root device block volume's associated filesystem volume.
func (d *powerflex) DefaultVMBlockFilesystemSize() string {
	return deviceConfig.DefaultVMPowerFlexBlockFilesystemSize
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *powerflex) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	// Activate PowerFlex volume if needed.
	volDevPath, cleanup, err := d.getMappedDevPath(vol, true)
	if err != nil {
		return err
	}

	revert.Add(cleanup)

	if vol.contentType == ContentTypeFS {
		mountPath := vol.MountPath()
		if !filesystem.IsMountPoint(mountPath) {
			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}

			fsType := vol.ConfigBlockFilesystem()

			if vol.mountFilesystemProbe {
				fsType, err = fsProbe(volDevPath)
				if err != nil {
					return fmt.Errorf("Failed probing filesystem: %w", err)
				}
			}

			mountFlags, mountOptions := filesystem.ResolveMountOptions(strings.Split(vol.ConfigBlockMountOptions(), ","))
			err = TryMount(volDevPath, mountPath, fsType, mountFlags, mountOptions)
			if err != nil {
				return err
			}

			d.logger.Debug("Mounted PowerFlex volume", logger.Ctx{"volName": vol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}
	} else if vol.contentType == ContentTypeBlock {
		// For VMs, mount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			err := d.MountVolume(fsVol, op)
			if err != nil {
				return err
			}
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	revert.Success()
	return nil
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
func (d *powerflex) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := vol.MountPath()
	refCount := vol.MountRefCountDecrement()

	// Attempt to unmount the volume.
	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err := TryUnmount(mountPath, unix.MNT_DETACH)
		if err != nil {
			return false, err
		}

		d.logger.Debug("Unmounted PowerFlex volume", logger.Ctx{"volName": vol.name, "path": mountPath, "keepBlockDev": keepBlockDev})

		// Attempt to unmap.
		if !keepBlockDev {
			err = d.unmapVolume(vol)
			if err != nil {
				return false, err
			}
		}

		ourUnmount = true
	} else if vol.contentType == ContentTypeBlock {
		// For VMs, unmount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}

		if !keepBlockDev {
			// Check if device is currently mapped (but don't map if not).
			devPath, _, _ := d.getMappedDevPath(vol, false)
			if devPath != "" && shared.PathExists(devPath) {
				if refCount > 0 {
					d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
					return false, ErrInUse
				}

				// Attempt to unmap.
				err := d.unmapVolume(vol)
				if err != nil {
					return false, err
				}

				ourUnmount = true
			}
		}
	}

	return ourUnmount, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *powerflex) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume in PowerFlex won't change it's name in storage.
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *powerflex) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	// When performing a cluster member move don't do anything on the source member.
	if volSrcArgs.ClusterMove {
		return nil
	}

	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume creates an exported version of a volume.
func (d *powerflex) BackupVolume(vol VolumeCopy, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *powerflex) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
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

	client := d.client()
	pool, err := d.resolvePool()
	if err != nil {
		return err
	}

	domain, err := client.getProtectionDomain(pool.ProtectionDomainID)
	if err != nil {
		return err
	}

	parentVolConfig := map[string]string{
		"volatile.uuid": snapVol.parentUUID,
	}

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, parentVolConfig, nil)
	parentVolName, err := d.getVolumeName(parentVol)
	if err != nil {
		return err
	}

	volumeID, err := client.getVolumeID(parentVolName)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	_, err = client.createVolumeSnapshot(domain.SystemID, volumeID, snapVolName, powerFlexSnapshotRW)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = d.DeleteVolumeSnapshot(snapVol, op) })

	// For VM images, create a filesystem volume too.
	if snapVol.IsVMBlock() {
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
func (d *powerflex) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	client := d.client()
	snapshotID, err := client.getVolumeID(snapVolName)
	if err != nil {
		return err
	}

	err = client.deleteVolume(snapshotID, "ONLY_ME")
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

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	// For VM images, delete the filesystem volume too.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountVolumeSnapshot simulates mounting a volume snapshot.
func (d *powerflex) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	// A snapshot in PowerFlex is just another volume.
	// We can reuse the volume mounting procedures.
	return d.MountVolume(snapVol, op)
}

// UnmountVolumeSnapshot simulates unmounting a volume snapshot.
func (d *powerflex) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	// A snapshot in PowerFlex is just another volume.
	// We can reuse the volume mounting procedures.
	return d.UnmountVolume(snapVol, false, op)
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *powerflex) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	client := d.client()
	volumeID, err := client.getVolumeID(volName)
	if err != nil {
		return nil, err
	}

	volumeSnapshots, err := client.getVolumeSnapshots(volumeID)
	if err != nil {
		return nil, err
	}

	var snapshotNames []string
	for _, snapshot := range volumeSnapshots {
		snapshotNames = append(snapshotNames, snapshot.Name)
	}

	return snapshotNames, nil
}

// CheckVolumeSnapshots checks that the volume's snapshots, according to the storage driver, match those provided.
func (d *powerflex) CheckVolumeSnapshots(vol Volume, snapVols []Volume, op *operations.Operation) error {
	// Get all of the volume's snapshots in base64 encoded format.
	storageSnapshotNames, err := vol.driver.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	// Create a list of all wanted snapshots.
	// The list contains the PowerFlex volume names in their base64 encoded format.
	wantedSnapshotNames := make([]string, 0, len(snapVols))
	for _, snap := range snapVols {
		snapName, err := d.getVolumeName(snap)
		if err != nil {
			return err
		}

		wantedSnapshotNames = append(wantedSnapshotNames, snapName)
	}

	// Check if the provided list of volume snapshots matches the ones from storage.
	for _, wantedSnapshotName := range wantedSnapshotNames {
		if !shared.ValueInSlice(wantedSnapshotName, storageSnapshotNames) {
			return fmt.Errorf("Snapshot %q expected but not in storage", wantedSnapshotName)
		}
	}

	// Check if the snapshots in storage match the ones from the provided list.
	for _, storageSnapshotName := range storageSnapshotNames {
		if !shared.ValueInSlice(storageSnapshotName, wantedSnapshotNames) {
			return fmt.Errorf("Snapshot %q in storage but not expected", storageSnapshotName)
		}
	}

	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *powerflex) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
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

	client := d.client()
	volumeID, err := client.getVolumeID(volName)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
	}

	snapshotID, err := client.getVolumeID(snapVolName)
	if err != nil {
		return err
	}

	err = client.overwriteVolume(volumeID, snapshotID)
	if err != nil {
		return err
	}

	// For VMs, also restore the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		snapFSVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.RestoreVolume(fsVol, snapFSVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *powerflex) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	// Renaming a volume snapshot in PowerFlex won't change it's name in storage.
	return nil
}
