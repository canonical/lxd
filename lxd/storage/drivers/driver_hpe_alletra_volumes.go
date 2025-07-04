package drivers

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
	"github.com/google/uuid"
)

// factorMiB divides a byte size value into Mebibytes.
const factorMiB = 1024 * 1024

// hpeAlletraVolTypePrefixes maps volume type to storage volume name prefix.
// Use smallest possible prefixes since Pure Storage volume names are limited to 63 characters.
var hpeAlletraVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: "c",
	VolumeTypeVM:        "v",
	VolumeTypeImage:     "i",
	VolumeTypeCustom:    "u",
}

// hpeAlletraContentTypeSuffixes maps volume's content type to storage volume name suffix.
var hpeAlletraContentTypeSuffixes = map[ContentType]string{
	// Suffix used for block content type volumes.
	ContentTypeBlock: "b",

	// Suffix used for ISO content type volumes.
	ContentTypeISO: "i",
}

// hpeAlletraSnapshotPrefix is a prefix used for Pure Storage snapshots to avoid name conflicts
// when creating temporary volume from the snapshot.
var hpeAlletraSnapshotPrefix = "s"

// defaultVMBlockFilesystemSize is the size of a VM root device block volume's associated filesystem volume.
const hpeAlletraVMBlockFilesystemSize = "256MiB"

// DefaultVMBlockFilesystemSize returns the size of a VM root device block volume's associated filesystem volume.
func (d *hpeAlletra) defaultVMBlockFilesystemSize() string {
	return hpeAlletraVMBlockFilesystemSize
}

// getVolumeName returns the fully qualified name derived from the volume's UUID.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) getVolumeName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`Failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}

	// Remove hypens from the UUID to create a volume name.
	volName := strings.ReplaceAll(volUUID.String(), "-", "")

	// Search for the volume type prefix, and if found, prepend it to the volume name.
	volumeTypePrefix, ok := hpeAlletraVolTypePrefixes[vol.volType]
	if ok {
		volName = volumeTypePrefix + "-" + volName
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	contentTypeSuffix, ok := hpeAlletraContentTypeSuffixes[vol.contentType]
	if ok {
		volName = volName + "-" + contentTypeSuffix
	}

	// If volume is snapshot, prepend snapshot prefix to its name.
	if vol.IsSnapshot() {
		volName = hpeAlletraSnapshotPrefix + volName
	}

	return volName, nil
}

// ensureHost returns a name of the host that is configured with a given IQN. If such host
// does not exist, a new one is created, where host's name equals to the server name with a
// mode included as a suffix because Pure Storage does not allow mixing IQNs, NQNs, and WWNs
// on a single host.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) ensureHost() (hostName string, cleanup revert.Hook, err error) {
	var hostname string

	revert := revert.New()
	defer revert.Fail()

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	// Get the qualified name of the host.
	qn, err := connector.QualifiedName()
	if err != nil {
		return "", nil, err
	}

	// Fetch an existing Pure Storage host.
	host, err := d.client().getCurrentHost()
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return "", nil, err
		}

		// The Pure Storage host with a qualified name of the current LXD host does not exist.
		// Therefore, create a new one and name it after the server name.
		serverName, err := ResolveServerName(d.state.ServerName)
		if err != nil {
			return "", nil, err
		}

		// Append the mode to the server name because Pure Storage does not allow mixing
		// NQNs, IQNs, and WWNs for a single host.
		hostname = serverName + "-" + connector.Type()

		err = d.client().createHost(hostname, []string{qn})
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusConflict) {
				return "", nil, err
			}

			// The host with the given name already exists, update it instead.
			err = d.client().updateHost(hostname, []string{qn})
			if err != nil {
				return "", nil, err
			}
		} else {
			revert.Add(func() { _ = d.client().deleteHost(hostname) })
		}
	} else {
		// Hostname already exists with the given IQN.
		hostname = host.Name
	}

	cleanup = revert.Clone().Fail
	revert.Success()
	return hostname, cleanup, nil
}

// mapVolume maps the given volume onto this host.
// TODO: copied as-is (with driver rename in one place) from pure. refactor?
func (d *hpeAlletra) mapVolume(vol Volume) (cleanup revert.Hook, err error) {
	reverter := revert.New()
	defer reverter.Fail()

	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return nil, err
	}

	defer unlock()

	// Ensure the host exists and is configured with the correct QN.
	hostname, cleanup, err := d.ensureHost()
	if err != nil {
		return nil, err
	}

	reverter.Add(cleanup)

	// Ensure the volume is connected to the host.
	connCreated, err := d.client().connectHostToVolume(vol.pool, volName, hostname)
	if err != nil {
		return nil, err
	}

	if connCreated {
		reverter.Add(func() { _ = d.client().disconnectHostFromVolume(vol.pool, volName, hostname) })
	}

	// Find the array's qualified name for the configured mode.
	targetQN, targetAddrs, err := d.client().getTarget()
	if err != nil {
		return nil, err
	}

	// Connect to the array.
	connReverter, err := connector.Connect(d.state.ShutdownCtx, targetQN, targetAddrs...)
	if err != nil {
		return nil, err
	}

	reverter.Add(connReverter)

	// If connect succeeded it means we have at least one established connection.
	// However, it's reverter does not cleanup the establised connections or a newly
	// created session. Therefore, if we created a mapping, add unmapVolume to the
	// returned (outer) reverter. Unmap ensures the target is disconnected only when
	// no other device is using it.
	outerReverter := revert.New()
	if connCreated {
		outerReverter.Add(func() { _ = d.unmapVolume(vol) })
	}

	// Add connReverter to the outer reverter, as it will immediately stop
	// any ongoing connection attempts. Note that it must be added after
	// unmapVolume to ensure it is called first.
	outerReverter.Add(connReverter)

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolume unmaps the given volume from this host.
// TODO: copied as-is (with driver rename in one place) from pure. refactor?
func (d *hpeAlletra) unmapVolume(vol Volume) error {
	connector, err := d.connector()
	if err != nil {
		return err
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return err
	}

	defer unlock()

	host, err := d.client().getCurrentHost()
	if err != nil {
		return err
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = d.client().disconnectHostFromVolume(vol.pool, volName, host.Name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	volumePath, _, _ := d.getMappedDevPath(vol, false)
	if volumePath != "" {
		// When iSCSI volume is disconnected from the host, the device will remain on the system.
		//
		// To remove the device, we need to either logout from the session or remove the
		// device manually. Logging out of the session is not desired as it would disconnect
		// from all connected volumes. Therefore, we need to manually remove the device.
		if connector.Type() == connectors.TypeISCSI {
			// removeDevice removes device from the system if the device is removable.
			removeDevice := func(devName string) error {
				path := "/sys/block/" + devName + "/device/delete"
				if shared.PathExists(path) {
					// Delete device.
					err := os.WriteFile(path, []byte("1"), 0400)
					if err != nil {
						return err
					}
				}

				return nil
			}

			devName := filepath.Base(volumePath)
			if strings.HasPrefix(devName, "dm-") {
				// Multipath device (/dev/dm-*) itself is not removable.
				// Therefore, we remove its slaves instead.
				slaves, err := filepath.Glob("/sys/block/" + devName + "/slaves/*")
				if err != nil {
					return fmt.Errorf("Failed to unmap volume %q: Failed to list slaves for device %q: %w", vol.name, devName, err)
				}

				// Remove slave devices.
				for _, slave := range slaves {
					slaveDevName := filepath.Base(slave)

					err := removeDevice(slaveDevName)
					if err != nil {
						return fmt.Errorf("Failed to unmap volume %q: Failed to remove slave device %q: %w", vol.name, slaveDevName, err)
					}
				}
			} else {
				// For non-multipath device (/dev/sd*), remove the device itself.
				err := removeDevice(devName)
				if err != nil {
					return fmt.Errorf("Failed to unmap volume %q: Failed to remove device %q: %w", vol.name, devName, err)
				}
			}
		}

		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		if !block.WaitDiskDeviceGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for Pure Storage volume %q to disappear on path %q", vol.name, volumePath)
		}
	}

	// If this was the last volume being unmapped from this system, disconnect the active session
	// and remove the host from HPE storage.

	//FIXME: it looks like we have no option than iterating over VLUNs to find out if there is any other active sessions

	return nil
}

// getMappedDevPath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
// TODO: copied as-is (except switch/case) from pure. refactor?
func (d *hpeAlletra) getMappedDevPath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	if mapVolume {
		cleanup, err := d.mapVolume(vol)
		if err != nil {
			return "", nil, err
		}

		revert.Add(cleanup)
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return "", nil, err
	}

	hpeVol, err := d.client().getVolume(vol.pool, volName)
	if err != nil {
		return "", nil, err
	}

	// Ensure the NGUID is exactly 32 characters long, as it uniquely
	// identifies the device. This check should never succeed, but prevents
	// out-of-bounds errors when slicing the string later.
	if len(hpeVol.NGUID) != 32 { //
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: Unexpected length of NGUID %q (%d)", vol.name, hpeVol.NGUID, len(hpeVol.NGUID))
	}

	var diskPrefix string
	var diskSuffix string

	//TODO: only this part was changed. Too many copied code. We need to generalize this.
	switch connector.Type() {
	case connectors.TypeISCSI:
		diskPrefix = "scsi-"
		diskSuffix = strings.ToLower(hpeVol.NGUID)
	case connectors.TypeNVME:
		diskPrefix = "nvme-eui."
		diskSuffix = strings.ToLower(hpeVol.NGUID)
	default:
		return "", nil, fmt.Errorf("Unsupported HPE Storage mode %q", connector.Type())
	}

	// Filters devices by matching the device path with the lowercase disk suffix.
	// Pure Storage reports serial numbers in uppercase, so the suffix is converted
	// to lowercase.
	diskPathFilter := func(devPath string) bool {
		return strings.HasSuffix(devPath, strings.ToLower(diskSuffix))
	}

	var devicePath string
	if mapVolume {
		// Wait until the disk device is mapped to the host.
		devicePath, err = block.WaitDiskDevicePath(d.state.ShutdownCtx, diskPrefix, diskPathFilter)
	} else {
		// Expect device to be already mapped.
		devicePath, err = block.GetDiskDevicePath(diskPrefix, diskPathFilter)
	}

	if err != nil {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: %w", vol.name, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return devicePath, cleanup, nil
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
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
func (d *hpeAlletra) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *hpeAlletra) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	return fmt.Errorf("CreateVolumeFromCopy: unsupported operation")
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *hpeAlletra) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	return fmt.Errorf("CreateVolumeFromMigration: unsupported operation")
}

// RefreshVolume provides same-pool volume and specific snapshots syncing functionality.
func (d *hpeAlletra) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	return fmt.Errorf("RefreshVolume: unsupported operation")
}

// DeleteVolume deletes the volume and all associated snapshots.
// TODO: copied as-is from pure. refactor?
// Q: why in some drivers it removes only volume without snapshots?
func (d *hpeAlletra) DeleteVolume(vol Volume, op *operations.Operation) error {
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
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) HasVolume(vol Volume) (bool, error) {
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

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *hpeAlletra) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-hpe-alletra; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-hpe-alletra; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-hpe-alletra; group=volume-conf; key=size)
		// Default storage volume size rounded to 256MiB. The minimum size is 256MiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("256MiB")),
	}
}

// ValidateVolume validates the supplied volume config.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	// When creating volumes from ISO images, round its size to the next multiple of 256MiB.
	if vol.ContentType() == ContentTypeISO {
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		// Get the volumes size in MiB.
		sizeMiB := int64(math.Ceil(float64(sizeBytes) / float64(factorMiB)))

		// Get the rest of the modulo operation.
		nonMultipleRest := sizeMiB % 256

		// Check how many times the given size can be divided by 256.
		multipleCount := sizeMiB / 256

		// If the given size is smaller than 256, create a volume with at least 256MiB.
		if nonMultipleRest != 0 {
			multipleCount++
		}

		vol.SetConfigSize(strconv.FormatInt(multipleCount*factorMiB*256, 10))
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
func (d *hpeAlletra) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	_, changed := changedConfig["size"]
	if changed {
		err := d.SetVolumeQuota(vol, changedConfig["size"], false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *hpeAlletra) GetVolumeUsage(vol Volume) (int64, error) {
	return 0, fmt.Errorf("GetVolumeUsage: unsupported operation")
}

// SetVolumeQuota applies a size limit on volume.
func (d *hpeAlletra) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return fmt.Errorf("SetVolumeQuota: unsupported operation")
}

// GetVolumeDiskPath returns the location of a root disk block device.
// TODO: copied as-is from pure/powerflex. refactor?
func (d *hpeAlletra) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevPath(vol, false)
		return devPath, err
	}

	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *hpeAlletra) ListVolumes() ([]Volume, error) {
	return nil, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) MountVolume(vol Volume, op *operations.Operation) error {
	return MountRemoteVolume(d, vol, op)
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
// TODO: copied as-is from pure. refactor?
func (d *hpeAlletra) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return UnmountRemoteVolume(d, vol, keepBlockDev, op)
}

// RenameVolume renames a volume and its snapshots.
func (d *hpeAlletra) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume in HPE Alletra won't change it's name in storage.
	return nil
}

// MigrateVolume sends a volume for migration.
func (d *hpeAlletra) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	// When performing a cluster member move don't do anything on the source member.
	if volSrcArgs.ClusterMove {
		return nil
	}

	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume creates an exported version of a volume.
func (d *hpeAlletra) BackupVolume(vol VolumeCopy, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *hpeAlletra) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return d.createVolumeSnapshot(snapVol, true, op)
}

// createVolumeSnapshot creates a snapshot of a volume. If snapshotVMfilesystem is false, a VM's filesystem volume
// is not copied.
func (d *hpeAlletra) createVolumeSnapshot(snapVol Volume, snapshotVMfilesystem bool, op *operations.Operation) error {
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

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *hpeAlletra) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentVol := snapVol.GetParent()
	parentVolName, err := d.getVolumeName(parentVol)
	if err != nil {
		return err
	}

	snapVolName, err := d.getVolumeName(snapVol)
	if err != nil {
		return err
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

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *hpeAlletra) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return fmt.Errorf("MountVolumeSnapshot: unsupported operation")
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *hpeAlletra) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	return false, fmt.Errorf("UnmountVolumeSnapshot: unsupported operation")
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *hpeAlletra) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return nil, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *hpeAlletra) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *hpeAlletra) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	// Renaming a volume snapshot won't change an actual name of the HPE Alletra volume snapshot.
	return nil
}

// getNVMeTargetQN discovers the targetQN used for the given addresses.
// The targetQN is unqiue per PowerFlex storage pool.
// Cache the targetQN as it doesn't change throughout the lifetime of the storage pool.
func (d *hpeAlletra) getNVMeTargetQN(targetAddresses ...string) (string, error) {
	if d.nvmeTargetQN == "" {
		connector, err := d.connector()
		if err != nil {
			return "", err
		}

		// The discovery log from the first reachable target address is returned.
		discoveryLogRecords, err := connectors.Discover(d.state.ShutdownCtx, connector, d.state.ServerUUID, targetAddresses...)
		if err != nil {
			return "", fmt.Errorf("Failed to discover SDT NQN: %w", err)
		}

		for _, record := range discoveryLogRecords {
			// The targetQN is listed together with every log record.
			d.nvmeTargetQN = record.SubNQN
			break
		}
	}

	return d.nvmeTargetQN, nil
}
