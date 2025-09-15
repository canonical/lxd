package drivers

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

	// Connect to the array or do a rescan to get a new volumes mapped.
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

	// Get a path of a block device we want to unmap.
	volumePath, _, _ := d.getMappedDevPath(vol, false)

	// When iSCSI volume is disconnected from the host, the device will remain on the system.
	//
	// To remove the device, we need to either logout from the session or remove the
	// device manually. Logging out of the session is not desired as it would disconnect
	// from all connected volumes. Therefore, we need to manually remove the device.
	//
	// Also, for iSCSI we don't want to unmap the device on the storage array side before removing it
	// from the host, because on some storage arrays (for example, HPE Alletra) we've seen that removing
	// a vLUN from the array immediately makes device inaccessible and traps any task tries to access it
	// to D-state (and this task can be systemd-udevd which tries to remove a device node!).
	// That's why it is better to remove the device node from the host and then remove vLUN.
	if volumePath != "" && connector.Type() == connectors.TypeISCSI {
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
			_, err := shared.RunCommandContext(d.state.ShutdownCtx, "multipath", "-f", volumePath)
			if err != nil {
				return fmt.Errorf("Failed to unmap volume %q: Failed to remove multipath device %q: %w", vol.name, devName, err)
			}
		} else {
			// For non-multipath device (/dev/sd*), remove the device itself.
			err := removeDevice(devName)
			if err != nil {
				return fmt.Errorf("Failed to unmap volume %q: Failed to remove device %q: %w", vol.name, devName, err)
			}
		}

		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		if !block.WaitDiskDeviceGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for HPE Alletra storage volume %q to disappear on path %q", vol.name, volumePath)
		}

		// Device is not there anymore.
		volumePath = ""
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = d.client().DisconnectHostFromVolume(vol.pool, volName, host.Name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	// When NVMe/TCP volume is disconnected from the host, the device automatically disappears.
	if volumePath != "" && connector.Type() == connectors.TypeNVME {
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

		// Disconnect from the array.
		// Do this first before removing the host from HPE Alletra.
		err = connector.Disconnect(targetQN)
		if err != nil {
			return err
		}

		// Delete the host from HPE Alletra if the last volume mapping got removed.
		// This requires the host to be already disconnected from the array.
		err = d.client().DeleteHost(host.Name)
		if err != nil {
			return err
		}

		// We have to invalidate a cached value of target qualified name as we've
		// disconnected from the array and removed the host. Experiment shows, that
		// after this, previous QN becomes invalid and we have to discovery again.
		d.targetQN = ""
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
		diskSuffix = strings.ToLower(hpeVol.WWN)
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
		err := d.SetVolumeQuota(v, v.config["size"], false, op)
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

		revert.Add(func() {
			err := d.DeleteVolume(fsVol.Volume, op)
			if err != nil {
				d.logger.Warn("DeleteVolume failed on error path", logger.Ctx{"err": err})
			}
		})
	}

	poolName := vol.pool

	volName, err := d.getVolumeName(vol.Volume)
	if err != nil {
		return err
	}

	srcVolName, err := d.getVolumeName(srcVol.Volume)
	if err != nil {
		return err
	}

	// Since snapshots are first copied into destination volume from which a new snapshot is created,
	// we need to also precreate and remove the destination volume if an error occurs during copying of snapshots.

	// Determine a destination volume size
	sizeBytes, err := units.ParseByteSizeString(vol.config["size"])
	if err != nil {
		return err
	}

	// Determine a source volume size on the array
	srcVolume, err := d.client().GetVolume(poolName, srcVolName)
	if err != nil {
		return err
	}

	// A tricky part here is that we need to carefully deal with volume size,
	// because HPE Alletra does not allow volume shrunk.
	if srcVolume.SizeMiB*factorMiB > sizeBytes {
		return ErrCannotBeShrunk
	}

	// Pre-create the target volume (empty).
	err = d.client().CreateVolume(vol.pool, volName, sizeBytes)
	if err != nil {
		return err
	}

	revert.Add(func() {
		err := d.client().DeleteVolume(vol.pool, volName)
		if err != nil {
			d.logger.Warn("DeleteVolume API call failed on error path", logger.Ctx{"err": err})
		}
	})

	// Copy volume snapshots.
	// HPE Alletra Storage does not allow copying snapshots along with the volume. Therefore, we
	// copy the snapshots sequentially. Each snapshot is first copied into destination
	// volume from which a new snapshot is created. The process is repeated until all
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

			// Copy the snapshot on the destination volume.
			err = d.client().CreateVolumePhysicalCopy(d.state.ShutdownCtx, poolName, srcSnapshotName, volName)
			if err != nil {
				return fmt.Errorf("Failed copying snapshot %q: %w", snapshot.name, err)
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

	err = d.client().CreateVolumePhysicalCopy(d.state.ShutdownCtx, poolName, srcVolName, volName)
	if err != nil {
		return err
	}

	err = postCreateTasks(vol.Volume)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *alletra) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
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
func (d *alletra) refreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) (revert.Hook, error) {
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
		err := d.SetVolumeQuota(v, v.config["size"], false, op)
		if err != nil {
			return err
		}

		return nil
	}

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
	err = d.client().DeleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	}

	// Create new reverter snapshot.
	err = d.client().CreateVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		// Restore destination volume from reverter snapshot and remove the snapshot afterwards.
		err := d.client().RestoreVolumeSnapshot(d.state.ShutdownCtx, vol.pool, volName, reverterSnapshotName)
		if err != nil {
			d.logger.Warn("RestoreVolumeSnapshot API call failed on error path", logger.Ctx{"err": err})
		}

		err = d.client().DeleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
		if err != nil {
			d.logger.Warn("DeleteVolumeSnapshot API call failed on error path", logger.Ctx{"err": err})
		}
	})

	if !srcVol.IsSnapshot() && len(refreshSnapshots) > 0 {
		var refreshedSnapshots []string

		// Refresh volume snapshots.
		// HPE Alletra Storage does not allow copying snapshots along with the volume. Therefore,
		// we copy the missing snapshots sequentially. Each snapshot is first copied into
		// destination volume from which a new snapshot is created. The process is repeated
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
			err = d.client().CreateVolumePhysicalCopy(d.state.ShutdownCtx, poolName, srcSnapshotName, volName)
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

			revert.Add(func() {
				err := d.DeleteVolumeSnapshot(snapshot, op)
				if err != nil {
					d.logger.Warn("DeleteVolumeSnapshot failed on error path", logger.Ctx{"err": err})
				}
			})

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
	err = d.client().CreateVolumePhysicalCopy(d.state.ShutdownCtx, poolName, srcVolName, volName)
	if err != nil {
		return nil, err
	}

	err = postCreateTasks(vol.Volume)
	if err != nil {
		return nil, err
	}

	cleanup := revert.Clone().Fail
	revert.Success()

	{
		// Remove temporary reverter snapshot.
		err := d.client().DeleteVolumeSnapshot(vol.pool, volName, reverterSnapshotName)
		if err != nil {
			d.logger.Error("DeleteVolumeSnapshot API call failed on error path", logger.Ctx{"err": err})
		}
	}

	return cleanup, err
}

// MigrateVolume sends a volume for migration.
func (d *alletra) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	// When performing a cluster member move don't do anything on the source member.
	if volSrcArgs.ClusterMove {
		return nil
	}

	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *alletra) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
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

	// If volume represents a snapshot, also retrieve (encoded) volume name of the parent,
	// and check if the snapshot exists.
	if vol.IsSnapshot() {
		parentVol := vol.GetParent()
		parentVolName, err := d.getVolumeName(parentVol)
		if err != nil {
			return false, err
		}

		_, err = d.client().GetVolumeSnapshot(vol.pool, parentVolName, volName)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return false, nil
			}

			return false, err
		}

		return true, nil
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

			err = block.RefreshDiskDeviceSize(d.state.ShutdownCtx, devPath)
			if err != nil {
				return fmt.Errorf("Failed refreshing volume %q size: %w", vol.name, err)
			}

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

		err = block.RefreshDiskDeviceSize(d.state.ShutdownCtx, devPath)
		if err != nil {
			return fmt.Errorf("Failed refreshing volume %q size: %w", vol.name, err)
		}

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

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *alletra) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return d.createVolumeSnapshot(snapVol, true, op)
}

// createVolumeSnapshot creates a snapshot of a volume. If snapshotVMfilesystem is false, a VM's filesystem volume
// is not copied.
func (d *alletra) createVolumeSnapshot(snapVol Volume, snapshotVMfilesystem bool, op *operations.Operation) error {
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
			defer func() {
				err := unfreezeFS()
				if err != nil {
					d.logger.Warn("unfreezeFS failed on error path", logger.Ctx{"err": err})
				}
			}()
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

	err = d.client().CreateVolumeSnapshot(snapVol.pool, parentVolName, snapVolName)
	if err != nil {
		return err
	}

	revert.Add(func() {
		err := d.DeleteVolumeSnapshot(snapVol, op)
		if err != nil {
			d.logger.Warn("DeleteVolumeSnapshot failed on error path", logger.Ctx{"err": err})
		}
	})

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

		revert.Add(func() {
			err := d.DeleteVolumeSnapshot(fsVol, op)
			if err != nil {
				d.logger.Warn("DeleteVolumeSnapshot failed on error path", logger.Ctx{"err": err})
			}
		})
	}

	revert.Success()
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *alletra) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
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
	err = d.client().DeleteVolumeSnapshot(snapVol.pool, parentVolName, snapVolName)
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

// RenameVolumeSnapshot renames a volume snapshot.
func (d *alletra) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	// Renaming a volume snapshot won't change an actual name of the HPE Alletra volume snapshot.
	return nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *alletra) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return mountVolume(d, snapVol, d.getMappedDevPath, op)
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *alletra) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	return unmountVolume(d, snapVol, false, d.getMappedDevPath, d.unmapVolume, op)
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *alletra) ListVolumes() ([]Volume, error) {
	// The reason for having this method to always return an empty array
	// is that we can't really get this information from a storage array.
	// Particularly, we need to know the original volume name, but we don't
	// have it stored on the array side as we use a UUID-based generated names
	// (see getVolumeName() method).
	return []Volume{}, nil
}

// VolumeSnapshots returns a list of HPE Alletra storage snapshot names for the given volume (in no particular order).
func (d *alletra) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	volumeSnapshots, err := d.client().GetVolumeSnapshots(vol.pool, volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, nil
		}

		return nil, err
	}

	snapshotNames := make([]string, 0, len(volumeSnapshots))
	for _, snapshot := range volumeSnapshots {
		snapshotNames = append(snapshotNames, snapshot.Name)
	}

	return snapshotNames, nil
}

// CheckVolumeSnapshots checks that the volume's snapshots, according to the storage driver,
// match those provided.
func (d *alletra) CheckVolumeSnapshots(vol Volume, snapVols []Volume, op *operations.Operation) error {
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

// RestoreVolume restores a volume from a snapshot.
func (d *alletra) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
	ourUnmount, err := d.UnmountVolume(vol, false, op)
	if err != nil {
		return err
	}

	if ourUnmount {
		defer func() {
			err := d.MountVolume(vol, op)
			if err != nil {
				d.logger.Warn("MountVolume failed on error path", logger.Ctx{"err": err})
			}
		}()
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
	err = d.client().RestoreVolumeSnapshot(d.state.ShutdownCtx, vol.pool, volName, snapVolName)
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
