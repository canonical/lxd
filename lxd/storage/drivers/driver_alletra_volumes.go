package drivers

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/operations"
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
	return 0, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
func (d *alletra) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return ErrNotSupported
}
