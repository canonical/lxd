package drivers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

const (
	powerStoreVolMinSize int64 = 1024 * 1024                     // 1 MiB in bytes.
	powerStoreVolMaxSize int64 = 256 * 1024 * 1024 * 1024 * 1024 // 256 TiB in bytes.
)

const (
	powerStoreVolPrefixSep            = "_" // Volume name prefix separator.
	powerStoreVolSuffixSep            = "." // Volume name suffix separator.
	powerStoreMountableSnapshotPrefix = "s" // Volume type prefix for mountable (temporary) snapshot clones.
)

// powerStoreVolTypePrefixes maps volume type to storage volume name prefix.
var powerStoreVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: "c",
	VolumeTypeVM:        "v",
	VolumeTypeImage:     "i",
	VolumeTypeCustom:    "u",
}

// powerStoreVolContentTypeSuffixes maps volume's content type to storage volume name suffix.
var powerStoreVolContentTypeSuffixes = map[ContentType]string{
	// Suffix used for block content type volumes.
	ContentTypeBlock: "b",

	// Suffix used for ISO content type volumes.
	ContentTypeISO: "i",
}

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
	if vol.IsSnapshot() {
		// Snapshots cannot be attached directly. The [powerstore.MountVolumeSnapshot]
		// maps a temporary clone, therefore, search for the device path of a snapshot
		// clone.
		cloneVol, err := d.newMountableSnapshotVolume(vol)
		if err != nil {
			return "", err
		}

		vol = cloneVol
	}

	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevicePath(vol, false)
		return devPath, err
	}

	return "", ErrNotSupported
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *powerstore) GetVolumeUsage(vol Volume) (int64, error) {
	return -1, ErrNotSupported
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *powerstore) HasVolume(vol Volume) (bool, error) {
	// Retrieve ID of the remote volume. If it succeeds, the volume exists.
	_, err := d.getVolumeID(vol)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
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
	return mountVolume(d, vol, d.getMappedDevicePath, progressReporter)
}

// UnmountVolume simulates unmounting a volume.
//
// keepBlockDev indicates whether the backing block device should be kept mapped to the
// host if the volume is unmounted.
func (d *powerstore) UnmountVolume(vol Volume, keepBlockDev bool, progressReporter ioprogress.ProgressReporter) (bool, error) {
	return unmountVolume(d, vol, keepBlockDev, d.getMappedDevicePath, d.unmapVolume, progressReporter)
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

// newMountableSnapshotVolume returns a non-snapshot [Volume] representing the temporary clone
// of snapVol which is used when snapshot needs to be mounted (mounting snapshot directly is not
// supported by PowerStore).
//
// The clone reuses the snapshot's own UUID. Its name is made unique by adding prefix
// [powerStoreMountableSnapshotPrefix] that [powerstore.encodeVolumeName] prepends to
// the volume type prefix.
func (d *powerstore) newMountableSnapshotVolume(snapVol Volume) (Volume, error) {
	snapUUID := snapVol.config["volatile.uuid"]
	_, err := uuid.Parse(snapUUID)
	if err != nil {
		return Volume{}, fmt.Errorf(`Failed parsing "volatile.uuid" from snapshot %q: %w`, snapVol.name, err)
	}

	// Prefix snapshot clone with to allow distinguishing it from regular volumes.
	cloneName := powerStoreMountableSnapshotPrefix + snapUUID

	return NewVolume(d, d.name, snapVol.volType, snapVol.contentType, cloneName, snapVol.config, d.config), nil
}

// MountVolumeSnapshot mounts a volume snapshot.
// Since PowerStore does not support mounting snapshots directly, this function creates a
// temporary clone of the snapshot and mounts the clone instead.
func (d *powerstore) MountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error {
	revert := revert.New()
	defer revert.Fail()

	client := d.client()

	snapVolID, err := d.getVolumeID(snapVol)
	if err != nil {
		return err
	}

	cloneVol, err := d.newMountableSnapshotVolume(snapVol)
	if err != nil {
		return err
	}

	cloneVolID, err := d.getVolumeID(cloneVol)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed checking for existing clone of snapshot %q: %w", snapVol.name, err)
	}

	if cloneVolID != "" {
		// Clone volume already exists, likely from a previous mount operation
		// did not get cleaned up properly.
		_ = client.DeleteVolume(cloneVolID)
	}

	cloneVolName, err := d.encodeVolumeName(cloneVol)
	if err != nil {
		return err
	}

	cloneVolID, err = client.CloneVolume(snapVolID, cloneVolName)
	if err != nil {
		return fmt.Errorf("Failed creating temporary clone for snapshot %q: %w", snapVol.name, err)
	}

	revert.Add(func() { _ = client.DeleteVolume(cloneVolID) })

	// For VMs, also clone the filesystem snapshot.
	if snapVol.IsVMBlock() {
		snapFsVol := snapVol.NewVMBlockFilesystemVolume()
		snapFsVol.SetParentUUID(snapVol.parentUUID)

		fsSnapVolClone, err := d.newMountableSnapshotVolume(snapFsVol)
		if err != nil {
			return err
		}

		// Clean up any stale filesystem clone.
		fsSnapVolCloneID, err := d.getVolumeID(fsSnapVolClone)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Failed checking for existing FS clone of snapshot %q: %w", snapVol.name, err)
		}

		if fsSnapVolCloneID != "" {
			_ = client.DeleteVolume(fsSnapVolCloneID)
		}

		fsSnapVolID, err := d.getVolumeID(snapFsVol)
		if err != nil {
			return err
		}

		fsSnapVolCloneName, err := d.encodeVolumeName(fsSnapVolClone)
		if err != nil {
			return err
		}

		fsSnapVolCloneID, err = client.CloneVolume(fsSnapVolID, fsSnapVolCloneName)
		if err != nil {
			return fmt.Errorf("Failed creating temporary FS clone for snapshot %q: %w", snapVol.name, err)
		}

		revert.Add(func() { _ = client.DeleteVolume(fsSnapVolCloneID) })
	}

	err = d.MountVolume(cloneVol, progressReporter)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// UnmountVolumeSnapshot unmounts the volume snapshot.
// Since PowerStore does not support mounting snapshots directly, this function unmounts a
// temporary clone of the snapshot and deletes the clone.
func (d *powerstore) UnmountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) (bool, error) {
	client := d.client()

	cloneVol, err := d.newMountableSnapshotVolume(snapVol)
	if err != nil {
		return false, err
	}

	ourUnmount, err := d.UnmountVolume(cloneVol, false, progressReporter)
	if err != nil {
		return false, err
	}

	if !ourUnmount {
		return false, nil
	}

	cloneID, err := d.getVolumeID(cloneVol)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return true, fmt.Errorf("Failed finding temporary clone for snapshot %q: %w", snapVol.name, err)
	}

	if err == nil {
		err = client.DeleteVolume(cloneID)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return true, fmt.Errorf("Failed deleting temporary clone for snapshot %q: %w", snapVol.name, err)
		}
	}

	// For VMs, also delete the filesystem clone.
	if snapVol.IsVMBlock() {
		snapFsVol := snapVol.NewVMBlockFilesystemVolume()
		snapFsVol.SetParentUUID(snapVol.parentUUID)

		fsSnapVolClone, err := d.newMountableSnapshotVolume(snapFsVol)
		if err != nil {
			return true, err
		}

		fsSnapVolCloneID, err := d.getVolumeID(fsSnapVolClone)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return true, fmt.Errorf("Failed finding temporary FS clone for snapshot %q: %w", snapVol.name, err)
		}

		if err == nil {
			err = client.DeleteVolume(fsSnapVolCloneID)
			if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
				return true, fmt.Errorf("Failed deleting temporary FS clone for snapshot %q: %w", snapVol.name, err)
			}
		}
	}

	return ourUnmount, nil
}

// encodeVolumeName derives the name of a volume resource in PowerStore from the provided volume.
func (d *powerstore) encodeVolumeName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`Failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}

	volName := volUUID.String()

	// Search for the volume type prefix, and if found, prepend it to the volume name.
	prefix := powerStoreVolTypePrefixes[vol.volType]
	if prefix != "" {
		// Mountable (temporary) snapshot clones have a distinct prefix. The clone
		// name is always exactly the mountable snapshot prefix followed by the
		// snapshot UUID. Retain prefix as it allows to distinguishing mountable
		// snapshot clones from regular volumes.
		if vol.name == powerStoreMountableSnapshotPrefix+volUUID.String() {
			prefix = powerStoreMountableSnapshotPrefix + prefix
		}

		volName = prefix + powerStoreVolPrefixSep + volName
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	suffix := powerStoreVolContentTypeSuffixes[vol.contentType]
	if suffix != "" {
		volName = volName + powerStoreVolSuffixSep + suffix
	}

	return d.storagePoolScopePrefix(vol.pool) + volName, nil
}

// decodeVolumeName decodes the PowerStore volume resource name and extracts the stored data.
func (d *powerstore) decodeVolumeName(name string) (volType VolumeType, volUUID uuid.UUID, volContentType ContentType, isMountableSnapshot bool, err error) {
	// Remove common resource prefix.
	poolAndVolName, ok := strings.CutPrefix(name, powerStoreResourcePrefix)
	if !ok {
		return "", uuid.Nil, "", false, fmt.Errorf("Failed decoding volume name %q: Missing LXD prefix", name)
	}

	// Remove storage pool prefix.
	poolName, volName, ok := strings.Cut(poolAndVolName, "-")
	if !ok || poolName == "" || volName == "" {
		return "", uuid.Nil, "", false, fmt.Errorf("Failed decoding volume name %q: Invalid name format", name)
	}

	// Volume prefix represents volume type.
	volPrefix, volNameWithoutPrefix, ok := strings.Cut(volName, powerStoreVolPrefixSep)
	if ok {
		volName = volNameWithoutPrefix

		// Mountable (temporary) snapshot clones have an additional prefix which must
		// be stripped before lookup.
		volPrefix, ok := strings.CutPrefix(volPrefix, powerStoreMountableSnapshotPrefix)
		if ok {
			isMountableSnapshot = true
		}

		for k, v := range powerStoreVolTypePrefixes {
			if v == volPrefix {
				volType = k
				break
			}
		}
	}

	// Volume suffix represents content type.
	volName, volSuffix, ok := strings.Cut(volName, powerStoreVolSuffixSep)
	if ok {
		for k, v := range powerStoreVolContentTypeSuffixes {
			if v == volSuffix {
				volContentType = k
				break
			}
		}
	}

	// The remaining string should be the UUID.
	volUUID, err = uuid.Parse(volName)
	if err != nil {
		return "", uuid.Nil, "", false, fmt.Errorf("Failed decoding volume name %q: %w", name, err)
	}

	return volType, volUUID, volContentType, isMountableSnapshot, nil
}

// getVolumeID returns the PowerStore ID for the given LXD volume or snapshot.
// For snapshots, it resolves the parent volume first and then fetches the snapshot by name.
func (d *powerstore) getVolumeID(vol Volume) (string, error) {
	client := d.client()

	volName, err := d.encodeVolumeName(vol)
	if err != nil {
		return "", err
	}

	if vol.IsSnapshot() {
		parentVol := vol.GetParent()
		parentVolName, err := d.encodeVolumeName(parentVol)
		if err != nil {
			return "", err
		}

		parentVolID, err := client.GetVolumeID(parentVolName)
		if err != nil {
			return "", fmt.Errorf("Failed resolving ID for snapshot parent volume %q: %w", parentVol.name, err)
		}

		snapshotID, err := client.GetVolumeSnapshotID(parentVolID, volName)
		if err != nil {
			return "", fmt.Errorf("Failed resolving ID for snapshot %q: %w", vol.name, err)
		}

		return snapshotID, nil
	}

	volID, err := client.GetVolumeID(volName)
	if err != nil {
		return "", fmt.Errorf("Failed resolving ID for volume %q: %w", vol.name, err)
	}

	return volID, nil
}

// ensureHost returns ID of the host configured with the given qualified name.
// If no such host exists, it creates a new host using the server name with the
// mode appended as the host name.
func (d *powerstore) ensureHost() (hostID string, cleanup revert.Hook, err error) {
	client := d.client()

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
	host, err := client.GetCurrentHost(connector.Type(), qn)
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
		hostname := serverName + "-" + connector.Type()

		hostID, err = client.CreateHost(hostname, connector.Type(), qn)
		if err != nil {
			return "", nil, fmt.Errorf("Failed creating host %q: %w", hostname, err)
		}

		revert.Add(func() { _ = client.DeleteHost(hostID) })
	} else {
		// Hostname already exists with the given qualified name.
		hostID = host.ID
	}

	cleanup = revert.Clone().Fail
	revert.Success()
	return hostID, cleanup, nil
}

// getMappedDevicePath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
func (d *powerstore) getMappedDevicePath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
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

	volID, err := d.getVolumeID(vol)
	if err != nil {
		return "", nil, err
	}

	psVol, err := d.client().GetVolume(volID)
	if err != nil {
		return "", nil, fmt.Errorf("Failed retrieving volume %q: %w", vol.name, err)
	}

	_, wwn, ok := strings.Cut(psVol.WWN, ".")
	if !ok {
		return "", nil, fmt.Errorf("Failed parsing WWN for volume %q: Invalid format %q", vol.name, psVol.WWN)
	}

	// Filters devices by matching the device path with the WWN.
	devicePathFilter := func(path string) bool {
		return strings.Contains(path, wwn)
	}

	var devicePath string
	if mapVolume {
		// Wait until the disk device is mapped to the host.
		devicePath, err = connector.WaitDiskDevicePath(d.state.ShutdownCtx, devicePathFilter)
	} else {
		// Expect device to be already mapped.
		devicePath, err = connector.GetDiskDevicePath(devicePathFilter)
	}

	if err != nil {
		return "", nil, fmt.Errorf("Failed locating device for volume %q: %w", vol.name, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return devicePath, cleanup, nil
}

// mapVolume maps the given volume onto this host.
func (d *powerstore) mapVolume(vol Volume) (cleanup revert.Hook, err error) {
	client := d.client()

	reverter := revert.New()
	defer reverter.Fail()

	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	volID, err := d.getVolumeID(vol)
	if err != nil {
		return nil, err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return nil, err
	}

	defer unlock()

	// Ensure the host exists and is configured with the correct QN.
	hostID, cleanup, err := d.ensureHost()
	if err != nil {
		return nil, err
	}

	reverter.Add(cleanup)

	// Ensure the volume is connected to the host.
	connCreated, err := client.AttachVolumeToHost(volID, hostID)
	if err != nil {
		return nil, fmt.Errorf("Failed attaching volume %q to host: %w", vol.name, err)
	}

	if connCreated {
		reverter.Add(func() { _ = client.DetachVolumeFromHost(volID, hostID) })
	}

	// Find the array's qualified name for the configured mode.
	targets, err := d.targets()
	if err != nil {
		return nil, err
	}

	outerReverter := revert.New()
	hasUnmapReverter := false

	// Connect to the array.
	for qualifiedName, addresses := range targets {
		connReverter, err := connector.Connect(d.state.ShutdownCtx, qualifiedName, addresses...)
		if err != nil {
			return nil, err
		}

		// If connect succeeded it means we have at least one established connection.
		// However, it's reverter does not cleanup the established connections or a newly
		// created session. Therefore, if we created a mapping, add unmapVolume to the
		// returned (outer) reverter. Unmap ensures the target is disconnected only when
		// no other device is using it.
		if connCreated && !hasUnmapReverter {
			outerReverter.Add(func() { _ = d.unmapVolume(vol) })
			hasUnmapReverter = true
		}

		// Add connReverter to the outer reverter, as it will immediately stop
		// any ongoing connection attempts. Note that it must be added after
		// unmapVolume to ensure it is called first.
		outerReverter.Add(connReverter)
		reverter.Add(connReverter)
	}

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolume unmaps the given volume from this host.
func (d *powerstore) unmapVolume(vol Volume) error {
	client := d.client()

	connector, err := d.connector()
	if err != nil {
		return err
	}

	qn, err := connector.QualifiedName()
	if err != nil {
		return err
	}

	volID, err := d.getVolumeID(vol)
	if err != nil {
		return err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return err
	}

	defer unlock()

	host, err := client.GetCurrentHost(connector.Type(), qn)
	if err != nil {
		return err
	}

	// Get a path of a block device we want to unmap.
	volumePath, _, _ := d.getMappedDevicePath(vol, false)

	// Remove disk device.
	if volumePath != "" {
		err = connector.RemoveDiskDevice(d.state.ShutdownCtx, volumePath)
		if err != nil {
			return fmt.Errorf("Failed unmapping PowerStore volume %q: %w", vol.name, err)
		}
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = client.DetachVolumeFromHost(volID, host.ID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed detaching volume %q from host %q: %w", vol.name, host.Name, err)
	}

	// Wait until the volume has disappeared.
	ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
	defer cancel()

	if volumePath != "" && !block.WaitDiskDeviceGone(ctx, volumePath) {
		return fmt.Errorf("Timeout exceeded waiting for PowerStore volume %q to disappear on path %q", vol.name, volumePath)
	}

	// If this was the last volume being unmapped from this system, disconnect the active session
	// and remove the host from PowerStore.
	if len(host.MappedVolumes) <= 1 {
		targets, err := d.targets()
		if err != nil {
			return err
		}

		var disconnectErr error
		for qualifiedName := range targets {
			// Disconnect from the target.
			err = connector.Disconnect(qualifiedName)
			if err != nil {
				disconnectErr = err
			}
		}

		if disconnectErr != nil {
			return fmt.Errorf("Failed disconnecting from targets after unmapping the last volume %q: %w", vol.name, disconnectErr)
		}

		// Remove the host from PowerStore.
		err = d.client().DeleteHost(host.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// roundVolumeBlockSizeBytes rounds the given size (in bytes) up to the next
// multiple of 1 MiB, which is the minimum volume size on PowerStore.
func (d *powerstore) roundVolumeBlockSizeBytes(_ Volume, sizeBytes int64) int64 {
	if sizeBytes < powerStoreVolMinSize {
		return powerStoreVolMinSize
	}

	return roundAbove(powerStoreVolMinSize, sizeBytes)
}
