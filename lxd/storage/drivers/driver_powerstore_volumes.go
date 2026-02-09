package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

const (
	powerStoreVolPrefixSep       = "_" // volume name prefix separator
	powerStoreContainerVolPrefix = "c" // volume name prefix indicating container volume
	powerStoreVMVolPrefix        = "v" // volume name prefix indicating virtual machine volume
	powerStoreImageVolPrefix     = "i" // volume name prefix indicating image volume
	powerStoreCustomVolPrefix    = "u" // volume name prefix indicating custom volume
)

// powerStoreVolTypePrefixes maps volume type to storage volume name prefix.
var powerStoreVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: powerStoreContainerVolPrefix,
	VolumeTypeVM:        powerStoreVMVolPrefix,
	VolumeTypeImage:     powerStoreImageVolPrefix,
	VolumeTypeCustom:    powerStoreCustomVolPrefix,
}

// powerStoreVolTypePrefixesRev maps storage volume name prefix to volume type.
var powerStoreVolTypePrefixesRev = map[string]VolumeType{
	powerStoreContainerVolPrefix: VolumeTypeContainer,
	powerStoreVMVolPrefix:        VolumeTypeVM,
	powerStoreImageVolPrefix:     VolumeTypeImage,
	powerStoreCustomVolPrefix:    VolumeTypeCustom,
}

const (
	powerStoreVolSuffixSep   = "." // volume name suffix separator
	powerStoreBlockVolSuffix = "b" // volume name suffix used for block content type volumes
	powerStoreISOVolSuffix   = "i" // volume name suffix used for iso content type volumes
)

// powerStoreVolContentTypeSuffixes maps volume content type to storage volume name suffix.
var powerStoreVolContentTypeSuffixes = map[ContentType]string{
	ContentTypeBlock: powerStoreBlockVolSuffix,
	ContentTypeISO:   powerStoreISOVolSuffix,
}

// powerStoreVolContentTypeSuffixesRev maps storage volume name suffix to volume content type.
var powerStoreVolContentTypeSuffixesRev = map[string]ContentType{
	powerStoreBlockVolSuffix: ContentTypeBlock,
	powerStoreISOVolSuffix:   ContentTypeISO,
}

// powerStoreResourceNamePrefix common prefix for all resource names in PowerStore.
const powerStoreResourceNamePrefix = "lxd:"

// powerStorePoolAndVolSep separates pool name and volume data in encoded volume names.
const powerStorePoolAndVolSep = "-"

// volumeResourceNamePrefix returns the prefix used by all volume resource names in PowerStore associated with the current storage pool.
func (d *powerstore) volumeResourceNamePrefix() string {
	poolHash := sha256.Sum256([]byte(d.Name()))
	poolName := base64.StdEncoding.EncodeToString(poolHash[:])
	return powerStoreResourceNamePrefix + poolName + powerStorePoolAndVolSep
}

// volumeResourceName derives the name of a volume resource in PowerStore from the provided volume.
func (d *powerstore) volumeResourceName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}
	volName := base64.StdEncoding.EncodeToString(volUUID[:])

	// Search for the volume type prefix, and if found, prepend it to the volume name.
	if prefix := powerStoreVolTypePrefixes[vol.volType]; prefix != "" {
		volName = prefix + powerStoreVolPrefixSep + volName
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	if suffix := powerStoreVolContentTypeSuffixes[vol.contentType]; suffix != "" {
		volName = volName + powerStoreVolSuffixSep + suffix
	}

	poolHash := sha256.Sum256([]byte(vol.Pool()))
	poolName := base64.StdEncoding.EncodeToString(poolHash[:])
	return powerStoreResourceNamePrefix + poolName + powerStorePoolAndVolSep + volName, nil
}

// extractDataFromVolumeResourceName decodes the PowerStore volume resource name and extracts the stored data.
func (d *powerstore) extractDataFromVolumeResourceName(name string) (poolHash string, volType VolumeType, volUUID uuid.UUID, volContentType ContentType, err error) {
	prefixLess, hasPrefix := strings.CutPrefix(name, powerStoreResourceNamePrefix)
	if !hasPrefix {
		return "", "", uuid.Nil, "", fmt.Errorf("failed to decode volume name %q: invalid name format", name)
	}
	poolHash, volName, ok := strings.Cut(prefixLess, powerStorePoolAndVolSep)
	if !ok || poolHash == "" || volName == "" {
		return "", "", uuid.Nil, "", fmt.Errorf("failed to decode volume name %q: invalid name format", name)
	}

	if prefix, volNameWithoutPrefix, ok := strings.Cut(volName, powerStoreVolPrefixSep); ok {
		volName = volNameWithoutPrefix
		volType = powerStoreVolTypePrefixesRev[prefix]
	}

	if volNameWithoutSuffix, suffix, ok := strings.Cut(volName, powerStoreVolSuffixSep); ok {
		volName = volNameWithoutSuffix
		volContentType = powerStoreVolContentTypeSuffixesRev[suffix]
	}

	binUUID, err := base64.StdEncoding.DecodeString(volName)
	if err != nil {
		return poolHash, volType, volUUID, volContentType, fmt.Errorf("failed to decode volume name %q: %w", name, err)
	}
	volUUID, err = uuid.FromBytes(binUUID)
	if err != nil {
		return poolHash, volType, volUUID, volContentType, fmt.Errorf("failed to parse UUID from decoded volume name: %w", err)
	}

	return poolHash, volType, volUUID, volContentType, nil
}

// volumeResourceName derives the name of a volume resource in PowerStore from the provided volume.
func (d *powerstore) volumeDeviceID(volResource *powerStoreVolumeResource) string {
	_, identifier, _ := strings.Cut(volResource.WWN, ".")
	return "wwn-0x" + identifier
}

// hostResourceName derives the name of a host resource in PowerStore associated with the current node or host and mode. On success, it returns name applicable to use as PowerStore host resource name along of full unencoded name.
func (d *powerstore) hostResourceName() (resource string, hostname string, err error) {
	hostname, err = ResolveServerName(d.state.ServerName)
	if err != nil {
		return "", "", err
	}
	resource = fmt.Sprintf("%s%s-%s/%s", powerStoreResourceNamePrefix, hostname, d.config["powerstore.mode"], d.config["powerstore.transport"])
	if len(resource) > 128 { // PowerStore limits host resource name to 128 characters
		hostnameHash := sha256.Sum256([]byte(hostname))
		resource = fmt.Sprintf("%s%s-%s/%s", powerStoreResourceNamePrefix, base64.StdEncoding.EncodeToString(hostnameHash[:]), d.config["powerstore.mode"], d.config["powerstore.transport"])
	}
	return resource, hostname, nil
}

// initiator returns PowerStore initiator resource associated the current host, mode and transport.
func (d *powerstore) initiator() (*powerStoreHostInitiatorResource, error) {
	if d.initiatorResource == nil {
		initiatorResource := &powerStoreHostInitiatorResource{}
		connector, err := d.connector()
		if err != nil {
			return nil, err
		}
		initiatorResource.PortName, err = connector.QualifiedName()
		if err != nil {
			return nil, err
		}
		switch {
		case d.config["powerstore.mode"] == connectors.TypeNVME:
			// PowerStore uses the same port type for both NVMe/TCP and NVMe/FC
			initiatorResource.PortType = powerStoreInitiatorPortTypeEnum_NVMe
		case d.config["powerstore.mode"] == connectors.TypeISCSI && d.config["powerstore.transport"] == "tcp":
			initiatorResource.PortType = powerStoreInitiatorPortTypeEnum_iSCSI
		case d.config["powerstore.mode"] == connectors.TypeISCSI && d.config["powerstore.transport"] == "fc":
			initiatorResource.PortType = powerStoreInitiatorPortTypeEnum_FC
		default:
			return nil, fmt.Errorf("cannot determine PowerStore initiator port type (mode: %q, transport: %q)", d.config["powerstore.mode"], d.config["powerstore.transport"])
		}
		d.initiatorResource = initiatorResource
	}
	return d.initiatorResource, nil
}

// roundVolumeBlockSizeBytes rounds the given size (in bytes) up to the next
// multiple of 1 MiB, which is the minimum volume size on PowerStore.
func (d *powerstore) roundVolumeBlockSizeBytes(_ Volume, sizeBytes int64) int64 {
	return roundAbove(powerStoreMinVolumeSizeBytes, sizeBytes)
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
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TB.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"size": validate.Optional(
			validate.IsNoLessThanUnit(powerStoreMinVolumeSizeUnit),
			validate.IsNoGreaterThanUnit(powerStoreMaxVolumeSizeUnit),
			validate.IsMultipleOfUnit(powerStoreMinVolumeSizeAlignmentUnit),
		),
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

// GetVolumeUsage returns the disk space used by the volume.
func (d *powerstore) GetVolumeUsage(vol Volume) (int64, error) {
	// If mounted, use the filesystem stats for pretty accurate usage information.
	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(vol.MountPath()) {
		var stat unix.Statfs_t
		err := unix.Statfs(vol.MountPath(), &stat)
		if err != nil {
			return -1, err
		}
		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	}

	volResource, err := d.getExistingVolumeResourceByVolume(vol)
	if err != nil {
		return -1, err
	}
	return volResource.LogicalUsed, nil
}

// SetVolumeQuota applies a size limit on volume.
func (d *powerstore) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return nil // TODO
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *powerstore) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, _, err := d.getMappedDevicePath(vol, false)
		return devPath, err
	}
	return "", ErrNotSupported
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *powerstore) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	volResource, err := d.createVolumeResource(vol)
	if err != nil {
		return err
	}
	revert.Add(func() { _ = d.deleteVolumeResource(volResource) })

	hostResource, err := d.getOrCreateHostWithInitiatorResource()
	if err != nil {
		return err
	}
	revert.Add(func() { _ = d.deleteHostAndInitiatorResource(hostResource) })

	if vol.contentType == ContentTypeFS {
		devPath, cleanup, err := d.getMappedDevicePath(vol, true)
		if err != nil {
			return err
		}
		revert.Add(cleanup)
		volumeFilesystem := vol.ConfigBlockFilesystem()
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

	mountTask := func(mountPath string, op *operations.Operation) error {
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

			// Run the filler.
			err = d.runFiller(vol, devPath, filler, true)
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
	}
	err = vol.MountTask(mountTask, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *powerstore) HasVolume(vol Volume) (bool, error) {
	volResource, err := d.getVolumeResourceByVolume(vol)
	if err != nil {
		return false, err
	}
	return volResource != nil, nil
}

// ListVolumes returns a list of LXD volumes in storage pool.
// It returns all volumes and sets the volume's volatile.uuid extracted from the name.
func (d *powerstore) ListVolumes() ([]Volume, error) {
	volResources, err := d.client().GetVolumes(context.Background())
	if err != nil {
		return nil, err
	}

	vols := make([]Volume, 0, len(volResources))
	for _, volResource := range volResources {
		_, volType, volUUID, volContentType, err := d.extractDataFromVolumeResourceName(volResource.Name)
		if err != nil {
			d.logger.Debug("Ignoring unrecognized volume", logger.Ctx{"name": volResource.Name, "err": err.Error()})
			continue
		}

		volConfig := map[string]string{
			"volatile.uuid": volUUID.String(),
		}
		vol := NewVolume(d, d.name, volType, volContentType, "", volConfig, d.config)
		if volContentType == ContentTypeFS {
			vol.SetMountFilesystemProbe(true)
		}
		vols = append(vols, vol)
	}

	return vols, nil
}

// UpdateVolume applies config changes to the volume.
func (d *powerstore) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// DeleteVolume deletes a volume of the storage device.
func (d *powerstore) DeleteVolume(vol Volume, op *operations.Operation) error {
	volResource, err := d.getVolumeResourceByVolume(vol)
	if err != nil {
		return err
	}
	if volResource != nil {
		err = d.deleteVolumeResource(volResource)
		if err != nil {
			return err
		}
	}

	hostResource, _, err := d.getHostWithInitiatorResource()
	if err != nil {
		return err
	}
	if hostResource != nil {
		err = d.deleteHostAndInitiatorResource(hostResource)
		if err != nil {
			return err
		}
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
		err = os.RemoveAll(mountPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %q directory: %w", mountPath, err)
		}
	}

	return nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *powerstore) MountVolume(vol Volume, op *operations.Operation) error {
	return mountVolume(d, vol, d.getMappedDevicePath, op)
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should not be unmapped if volume is unmounted.
func (d *powerstore) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return unmountVolume(d, vol, keepBlockDev, d.getMappedDevicePath, d.unmapVolume, op)
}

// RenameVolume renames a volume and its snapshots.
func (d *powerstore) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Renaming a volume in PowerStore will not change the name of the associated volume resource.
	return nil
}

// mapVolumeByVolumeResource maps the volume associated with the given PowerSore volume resource onto this host.
func (d *powerstore) mapVolumeByVolumeResource(volResource *powerStoreVolumeResource) (revert.Hook, error) {
	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	reverter := revert.New()
	defer reverter.Fail()

	hostResource, err := d.getOrCreateHostWithInitiatorResource()
	if err != nil {
		return nil, err
	}
	reverter.Add(func() { _ = d.deleteHostAndInitiatorResource(hostResource) })

	mapped := false
	for _, mappingResource := range volResource.MappedVolumes {
		if mappingResource.HostID == hostResource.ID {
			mapped = true
		}
	}
	if !mapped {
		if err := d.client().AttachHostToVolume(context.Background(), hostResource.ID, volResource.ID); err != nil {
			return nil, err
		}
		reverter.Add(func() { _ = d.client().DetachHostFromVolume(context.Background(), hostResource.ID, volResource.ID) })
	}

	targetQN, targetAddresses, err := d.target()
	if err != nil {
		return nil, err
	}
	cleanup, err := connector.Connect(d.state.ShutdownCtx, targetQN, targetAddresses...)
	if err != nil {
		return nil, err
	}

	// Reverting mapping or connection outside mapVolume function
	// could conflict with other ongoing operations as lock will
	// already be released. Therefore, use unmapVolume instead
	// because it ensures the lock is acquired and accounts for
	// an existing session before unmapping a volume.
	outerReverter := revert.New()
	if !mapped {
		outerReverter.Add(func() { _ = d.unmapVolumeByVolumeResource(volResource) })
	}
	outerReverter.Add(cleanup)

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolumeByVolumeResource unmaps the volume associated with the given PowerSore volume resource from this host.
func (d *powerstore) unmapVolumeByVolumeResource(volResource *powerStoreVolumeResource) error {
	connector, err := d.connector()
	if err != nil {
		return err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return err
	}
	defer unlock()

	hostResource, _, err := d.getHostWithInitiatorResource()
	if err != nil {
		return err
	}

	if hostResource != nil && volResource != nil {
		for _, mappingResource := range volResource.MappedVolumes {
			if mappingResource.HostID != hostResource.ID {
				continue
			}
			err := d.client().DetachHostFromVolume(context.Background(), mappingResource.HostID, volResource.ID)
			if err != nil {
				return err
			}
		}
	}

	// Disconnect connector if:
	// - there is no associated PowerStore host resource,
	// - there are no other volumes mapped.
	if hostResource == nil || len(hostResource.MappedHosts) == 0 {
		targetQN, _, err := d.target()
		if err != nil {
			return err
		}
		err = connector.Disconnect(targetQN)
		if err != nil {
			return err
		}
	}

	if hostResource != nil {
		err = d.deleteHostAndInitiatorResource(hostResource)
		if err != nil {
			return err
		}
	}

	return nil
}

// unmapVolume unmaps the given volume from this host.
func (d *powerstore) unmapVolume(vol Volume) error {
	volResource, err := d.getExistingVolumeResourceByVolume(vol)
	if err != nil {
		return err
	}
	return d.unmapVolumeByVolumeResource(volResource)
}

// getMappedDevicePath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
func (d *powerstore) getMappedDevicePath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	volResource, err := d.getExistingVolumeResourceByVolume(vol)
	if err != nil {
		return "", nil, err
	}
	volDeviceID := d.volumeDeviceID(volResource)

	if mapVolume {
		cleanup, err := d.mapVolumeByVolumeResource(volResource)
		if err != nil {
			return "", nil, err
		}
		revert.Add(cleanup)
	}

	devicePathFilter := func(path string) bool {
		return strings.Contains(path, volDeviceID)
	}
	var devicePath string
	if mapVolume {
		// Wait for the device path to appear as the volume has been just mapped to the host.
		devicePath, err = block.WaitDiskDevicePath(d.state.ShutdownCtx, "wwn-0x", devicePathFilter)
	} else {
		// Get the the device path without waiting.
		devicePath, err = block.GetDiskDevicePath("wwn-0x", devicePathFilter)
	}

	if err != nil {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: %w", vol.name, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return devicePath, cleanup, nil
}

// getVolumeResourceByVolume retrieves volume resource associated with the provided volume.
func (d *powerstore) getVolumeResourceByVolume(vol Volume) (*powerStoreVolumeResource, error) {
	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}
	return d.client().GetVolumeByName(context.Background(), volResourceName)
}

// getExistingVolumeResourceByVolume retrieves volume resource associated with the provided volume, just like getVolumeResourceByVolume function, but returns error if the volume resource does not exists.
func (d *powerstore) getExistingVolumeResourceByVolume(vol Volume) (*powerStoreVolumeResource, error) {
	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}
	volResource, err := d.client().GetVolumeByName(context.Background(), volResourceName)
	if err != nil {
		return nil, err
	}
	if volResource == nil {
		return nil, fmt.Errorf("PowerStore volume resource %q not found", volResourceName)
	}
	return volResource, nil
}

// createVolumeResource creates volume resources in PowerStore associated with the provided volume.
func (d *powerstore) createVolumeResource(vol Volume) (*powerStoreVolumeResource, error) {
	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return nil, err
	}
	if sizeBytes < powerStoreMinVolumeSizeBytes {
		return nil, fmt.Errorf("Volume size is too small, supported minimum %s", powerStoreMinVolumeSizeUnit)
	}
	if sizeBytes > powerStoreMaxVolumeSizeBytes {
		return nil, fmt.Errorf("Volume size is too large, supported maximum %s", powerStoreMaxVolumeSizeUnit)
	}

	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}

	typ := "lxd"
	if vol.volType != "" {
		typ += ":" + string(vol.volType)
	}
	if vol.contentType != "" {
		typ += ":" + string(vol.contentType)
	}

	volResource := &powerStoreVolumeResource{
		Name:         volResourceName,
		Description:  powerStoreSprintfLimit(128, "LXD Name: %s", vol.name), // maximum allowed value length for volume description field is 128
		Size:         sizeBytes,
		AppType:      "Other",
		AppTypeOther: powerStoreSprintfLimit(32, "%s", typ), // maximum allowed value length for volume app_type_other field is 32,
	}
	err = d.client().CreateVolume(context.Background(), volResource)
	if err != nil {
		return nil, err
	}
	return volResource, nil
}

// deleteVolumeResource deletes volume resources in PowerStore.
func (d *powerstore) deleteVolumeResource(volResource *powerStoreVolumeResource) error {
	for _, mappingResource := range volResource.MappedVolumes {
		err := d.client().DetachHostFromVolume(context.Background(), mappingResource.HostID, volResource.ID)
		if err != nil {
			return err
		}
	}
	for _, volumeGroupResource := range volResource.VolumeGroups {
		err := d.client().RemoveMembersFromVolumeGroup(context.Background(), volumeGroupResource.ID, []string{volResource.ID})
		if err != nil {
			return err
		}
	}
	return d.client().DeleteVolumeByID(context.Background(), volResource.ID)
}

// getHostWithInitiatorResource retrieves initiator and associated host resources from PowerStore associated with the current host, mode and transport.
func (d *powerstore) getHostWithInitiatorResource() (*powerStoreHostResource, *powerStoreHostInitiatorResource, error) {
	initiatorResource, err := d.initiator()
	if err != nil {
		return nil, nil, err
	}

	hostResource, err := d.client().GetHostByInitiator(context.Background(), initiatorResource)
	if err != nil {
		return nil, nil, err
	}
	if hostResource != nil {
		// host with initiator already exists
		return hostResource, initiatorResource, nil
	}

	// no initiator found
	hostResourceName, _, err := d.hostResourceName()
	if err != nil {
		return nil, nil, err
	}
	hostResource, err = d.client().GetHostByName(context.Background(), hostResourceName)
	if err != nil {
		return nil, nil, err
	}
	if hostResource != nil {
		// host without initiator found
		return hostResource, nil, nil
	}

	// no host or initiator exists
	return nil, nil, nil
}

// getOrCreateHostWithInitiatorResource retrieves (or creates if missing) initiator and associated host resources in PowerStore associated with the current host, mode and transport.
func (d *powerstore) getOrCreateHostWithInitiatorResource() (*powerStoreHostResource, error) {
	hostResource, initiatorResource, err := d.getHostWithInitiatorResource()
	if err != nil {
		return nil, err
	}

	if hostResource == nil {
		// no host or initiator exists
		initiatorResource, err = d.initiator()
		if err != nil {
			return nil, err
		}
		hostResourceName, hostname, err := d.hostResourceName()
		if err != nil {
			return nil, err
		}
		hostResource = &powerStoreHostResource{
			Name:        hostResourceName,
			Description: powerStoreSprintfLimit(256, "LXD Name: %s", hostname), // maximum allowed value length for host description field is 256
			OsType:      powerStoreOsTypeEnum_Linux,
			Initiators:  []*powerStoreHostInitiatorResource{initiatorResource},
		}
		err = d.client().CreateHost(context.Background(), hostResource)
		if err != nil {
			return nil, err
		}
		return hostResource, nil
	}

	if initiatorResource == nil {
		// host exists but initiator is missing
		initiatorResource, err = d.initiator()
		if err != nil {
			return nil, err
		}
		err = d.client().AddInitiatorToHostByID(context.Background(), hostResource.ID, initiatorResource)
		if err != nil {
			return nil, err
		}
		return d.client().GetHostByID(context.Background(), hostResource.ID) // refetch to refresh the data
	}

	// host with initiator already exists
	return hostResource, nil
}

// deleteHostAndInitiatorResource deletes initiator and associated host resources in PowerStore if there are no mapped (attached) volumes.
func (d *powerstore) deleteHostAndInitiatorResource(hostResource *powerStoreHostResource) error {
	initiatorResource, err := d.initiator()
	if err != nil {
		return err
	}
	hostResourceName, _, err := d.hostResourceName()
	if err != nil {
		return err
	}

	if len(hostResource.MappedHosts) > 0 {
		// host has some other volumes mapped
		return nil
	}
	if len(hostResource.Initiators) > 1 {
		// host has multiple initiators associated
		return nil
	}
	if len(hostResource.Initiators) == 1 && (hostResource.Initiators[0].PortName != initiatorResource.PortName || hostResource.Initiators[0].PortType != initiatorResource.PortType) {
		// associated initiator do not matches the expected one
		return nil
	}
	if hostResource.Name != hostResourceName {
		// host is not managed by LXD
		return nil
	}
	return d.client().DeleteHostByID(context.Background(), hostResource.ID)
}
