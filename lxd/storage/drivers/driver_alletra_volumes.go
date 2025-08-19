package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// factorMiB divides a byte size value into Mebibytes.
const factorMiB = 1024 * 1024

// alletraVolTypePrefixes maps volume type to storage volume name prefix.
var alletraVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: "c",
	VolumeTypeVM:        "v",
	VolumeTypeImage:     "i",
	VolumeTypeCustom:    "u",
}

// alletraContentTypeSuffixes maps volume's content type to storage volume name suffix.
var alletraContentTypeSuffixes = map[ContentType]string{
	// Suffix used for block content type volumes.
	ContentTypeBlock: "b",

	// Suffix used for ISO content type volumes.
	ContentTypeISO: "i",
}

// alletraSnapshotPrefix is a prefix used for HPE Alletra Storage snapshots to avoid name conflicts
// when creating temporary volume from the snapshot.
var alletraSnapshotPrefix = "s"

// defaultVMBlockFilesystemSize is the size of a VM root device block volume's associated filesystem volume.
const alletraVMBlockFilesystemSize = "256MiB"

// DefaultVMBlockFilesystemSize returns the size of a VM root device block volume's associated filesystem volume.
func (d *alletra) defaultVMBlockFilesystemSize() string {
	return alletraVMBlockFilesystemSize
}

// getVolumeName returns the fully qualified name derived from the volume's UUID.
func (d *alletra) getVolumeName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`Failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}

	// Remove hypens from the UUID to create a volume name.
	volName := strings.ReplaceAll(volUUID.String(), "-", "")

	// Search for the volume type prefix, and if found, prepend it to the volume name.
	volumeTypePrefix, ok := alletraVolTypePrefixes[vol.volType]
	if ok {
		volName = volumeTypePrefix + "-" + volName
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	contentTypeSuffix, ok := alletraContentTypeSuffixes[vol.contentType]
	if ok {
		volName = volName + "-" + contentTypeSuffix
	}

	// If volume is snapshot, prepend snapshot prefix to its name.
	if vol.IsSnapshot() {
		volName = alletraSnapshotPrefix + volName
	}

	return volName, nil
}

// ensureHost returns a name of the host that is configured with a given IQN. If such host
// does not exist, a new one is created, where host's name equals to the server name with a
// mode included.
func (d *alletra) ensureHost() (hostName string, cleanup revert.Hook, err error) {
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

	// Fetch an existing host entry on a storage array.
	host, err := d.client().GetCurrentHost(connector.Type(), qn)
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return "", nil, err
		}

		// The storage host entry with a qualified name of the current LXD host does not exist.
		// Therefore, create a new one and name it after the server name.
		serverName, err := ResolveServerName(d.state.ServerName)
		if err != nil {
			return "", nil, err
		}

		// Append the mode to the server name because storage array does not allow mixing
		// NQNs, IQNs, and WWNs for a single host.
		hostname = serverName + "-" + connector.Type()

		err = d.client().CreateHost(connector.Type(), hostname, []string{qn})
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusConflict) {
				return "", nil, err
			}

			// The host with the given name already exists, update it instead.
			err = d.client().UpdateHost(hostname, []string{qn})
			if err != nil {
				return "", nil, err
			}
		} else {
			revert.Add(func() {
				err := d.client().DeleteHost(hostname)
				if err != nil {
					d.logger.Warn("DeleteHost API call failed on error path", logger.Ctx{"err": err, "hostname": hostname})
				}
			})
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
func (d *alletra) mapVolume(vol Volume) (cleanup revert.Hook, err error) {
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
	connCreated, err := d.client().ConnectHostToVolume(vol.pool, volName, hostname)
	if err != nil {
		return nil, err
	}

	if connCreated {
		reverter.Add(func() {
			err := d.client().DisconnectHostFromVolume(vol.pool, volName, hostname)
			if err != nil {
				d.logger.Warn("DisconnectHostFromVolume API call failed on error path", logger.Ctx{"err": err, "volName": volName, "hostname": hostname})
			}
		})
	}

	// Find the array's qualified name for the configured mode.
	targetQN, targetAddrs, err := d.getTarget()
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
		outerReverter.Add(func() {
			err := d.unmapVolume(vol)
			if err != nil {
				d.logger.Warn("unmapVolume failed on error path", logger.Ctx{"err": err})
			}
		})
	}

	// Add connReverter to the outer reverter, as it will immediately stop
	// any ongoing connection attempts. Note that it must be added after
	// unmapVolume to ensure it is called first.
	outerReverter.Add(connReverter)

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolume unmaps the given volume from this host.
func (d *alletra) unmapVolume(vol Volume) error {
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

	qn, err := connector.QualifiedName()
	if err != nil {
		return err
	}

	host, err := d.client().GetCurrentHost(connector.Type(), qn)
	if err != nil {
		return err
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = d.client().DisconnectHostFromVolume(vol.pool, volName, host.Name)
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
			return fmt.Errorf("Timeout exceeded waiting for HPE Alletra storage volume %q to disappear on path %q", vol.name, volumePath)
		}
	}

	mappings, err := d.client().GetVLUNsForHost(host.Name)
	if err != nil {
		return err
	}

	// If this was the last volume being unmapped from this system, disconnect the active session
	// and remove the host from HPE storage.
	if len(mappings) == 0 {
		// Find the array's qualified name for the configured mode.
		targetQN, _, err := d.getTarget()
		if err != nil {
			return err
		}

		// Disconnect from the NVMe subsystem.
		// Do this first before removing the host from HPE Alletra.
		err = connector.Disconnect(targetQN)
		if err != nil {
			return err
		}

		// Delete the host from HPE Alletra if the last volume mapping got removed.
		// This requires the host to be already disconnected from the NVMe subsystem.
		err = d.client().DeleteHost(host.Name)
		if err != nil {
			return err
		}

		// We have to invalidate a cached value of NVMe target qualified name as we've
		// disconnected from the array and removed the host. Experiment shows, that
		// after this, previous QN becomes invalid and we have to do NVMe discovery.
		d.nvmeTargetQN = ""
	}

	return nil
}

// getMappedDevPath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
func (d *alletra) getMappedDevPath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
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

	hpeVol, err := d.client().GetVolume(vol.pool, volName)
	if err != nil {
		return "", nil, err
	}

	// Ensure the NGUID is exactly 32 characters long, as it uniquely
	// identifies the device. This check should never succeed, but prevents
	// out-of-bounds errors when slicing the string later.
	if len(hpeVol.NGUID) != 32 {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: Unexpected length of NGUID %q (%d)", vol.name, hpeVol.NGUID, len(hpeVol.NGUID))
	}

	var diskPrefix string
	var diskSuffix string

	switch connector.Type() {
	case connectors.TypeISCSI:
		diskPrefix = "scsi-"
		diskSuffix = strings.ToLower(hpeVol.NGUID)
	case connectors.TypeNVME:
		diskPrefix = "nvme-eui."
		diskSuffix = strings.ToLower(hpeVol.NGUID)
	default:
		return "", nil, fmt.Errorf("Unsupported Alletra Storage mode %q", connector.Type())
	}

	// Filters devices by matching the device path with the lowercase disk suffix.
	// storage reports serial numbers in uppercase, so the suffix is converted
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

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *alletra) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevPath(vol, false)
		return devPath, err
	}

	return "", ErrNotSupported
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *alletra) MountVolume(vol Volume, op *operations.Operation) error {
	return mountVolume(d, vol, d.getMappedDevPath, op)
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
func (d *alletra) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return unmountVolume(d, vol, keepBlockDev, d.getMappedDevPath, d.unmapVolume, op)
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *alletra) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
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
	err = client.CreateVolume(vol.pool, volName, sizeBytes)
	if err != nil {
		return err
	}

	revert.Add(func() {
		err := client.DeleteVolume(vol.pool, volName)
		if err != nil {
			d.logger.Warn("DeleteVolume API call failed on error path", logger.Ctx{"err": err})
		}
	})

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

		revert.Add(func() {
			err := d.DeleteVolume(fsVol, op)
			if err != nil {
				d.logger.Warn("DeleteVolume failed on error path", logger.Ctx{"err": err})
			}
		})
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
func (d *alletra) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state, vol, srcBackup.Snapshots, srcData, op)
}

// BackupVolume creates an exported version of a volume.
func (d *alletra) BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *alletra) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	return errors.New("CreateVolumeFromCopy: unsupported operation")
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *alletra) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	return errors.New("CreateVolumeFromMigration: unsupported operation")
}

// DeleteVolume deletes the volume and all associated snapshots.
func (d *alletra) DeleteVolume(vol Volume, op *operations.Operation) error {
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

	connector, err := d.connector()
	if err != nil {
		return err
	}

	// Get the qualified name of the host.
	qn, err := connector.QualifiedName()
	if err != nil {
		return err
	}

	host, err := d.client().GetCurrentHost(connector.Type(), qn)
	if err != nil {
		// If the host doesn't exist, continue with the deletion of
		// the volume and do not try to delete the volume mapping as
		// it cannot exist.
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	} else {
		// Delete the volume mapping with the host.
		err = d.client().DisconnectHostFromVolume(vol.pool, volName, host.Name)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	err = d.client().DeleteVolume(vol.pool, volName)
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
func (d *alletra) HasVolume(vol Volume) (bool, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return false, err
	}

	// Otherwise, check if the volume exists.
	_, err = d.client().GetVolume(vol.pool, volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *alletra) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume won't change an actual name of the volume on the storage array side.
	return nil
}

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *alletra) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-alletra; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-alletra; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-alletra; group=volume-conf; key=size)
		// Default storage volume size rounded to 256MiB. The minimum size is 256MiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("256MiB")),
	}
}

// FillVolumeConfig populate volume with default config.
func (d *alletra) FillVolumeConfig(vol Volume) error {
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
func (d *alletra) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
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
func (d *alletra) UpdateVolume(vol Volume, changedConfig map[string]string) error {
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
func (d *alletra) GetVolumeUsage(vol Volume) (int64, error) {
	// If mounted, use the filesystem stats for pretty accurate usage information.
	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(vol.MountPath()) {
		var stat unix.Statfs_t

		err := unix.Statfs(vol.MountPath(), &stat)
		if err != nil {
			return -1, err
		}

		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return -1, err
	}

	volume, err := d.client().GetVolume(vol.pool, volName)
	if err != nil {
		return -1, err
	}

	return volume.TotalUsedMiB * factorMiB, nil
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size.
func (d *alletra) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	client := d.client()
	volume, err := client.GetVolume(vol.pool, volName)
	if err != nil {
		return err
	}

	// Try to fetch the current size of the volume from the API.
	// If the volume is not yet mapped to the system this speeds up the
	// process as the volume doesn't have to be mapped to get its size
	// from the actual block device.
	oldSizeBytes := volume.SizeMiB * factorMiB

	// Do nothing if volume is already specified size (+/- 512 bytes).
	if oldSizeBytes+512 > sizeBytes && oldSizeBytes-512 < sizeBytes {
		return nil
	}

	// HPE Alletra supports increasing of size only.
	if sizeBytes < oldSizeBytes {
		return ErrCannotBeShrunk
	}

	// Resize filesystem if needed.
	if vol.contentType == ContentTypeFS {
		fsType := vol.ConfigBlockFilesystem()

		if sizeBytes > oldSizeBytes {
			// Grow block device first.
			err = client.GrowVolume(vol.pool, volName, sizeBytes-oldSizeBytes)
			if err != nil {
				return err
			}

			devPath, cleanup, err := d.getMappedDevPath(vol, true)
			if err != nil {
				return err
			}

			defer cleanup()

			// Always wait for the disk to reflect the new size.
			// In case SetVolumeQuota is called on an already mapped volume,
			// it might take some time until the actual size of the device is reflected on the host.
			// This is for example the case when creating a volume and the filler performs a resize in case the image exceeds the volume's size.
			err = block.WaitDiskDeviceResize(d.state.ShutdownCtx, devPath, sizeBytes)
			if err != nil {
				return fmt.Errorf("Failed waiting for volume %q to change its size: %w", vol.name, err)
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
		err = client.GrowVolume(vol.pool, volName, sizeBytes-oldSizeBytes)
		if err != nil {
			return err
		}

		devPath, cleanup, err := d.getMappedDevPath(vol, true)
		if err != nil {
			return err
		}

		defer cleanup()

		err = block.WaitDiskDeviceResize(d.state.ShutdownCtx, devPath, sizeBytes)
		if err != nil {
			return fmt.Errorf("Failed waiting for volume %q to change its size: %w", vol.name, err)
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
