package drivers

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/operations"
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

// GetVolumeUsage returns the disk space used by the volume.
func (d *powerstore) GetVolumeUsage(vol Volume) (int64, error) {
	return 0, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
func (d *powerstore) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return ErrNotSupported
}
