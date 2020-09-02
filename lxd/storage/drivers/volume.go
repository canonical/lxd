package drivers

import (
	"fmt"
	"os"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/locking"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
)

// tmpVolSuffix Suffix to use for any temporary volumes created by LXD.
const tmpVolSuffix = ".lxdtmp"

// defaultBlockSize Default size of block volumes.
const defaultBlockSize = "10GB"

// vmBlockFilesystemSize is the size of a VM block volume's associated filesystem volume.
const vmBlockFilesystemSize = "100MB"

// DefaultFilesystem filesytem to use for block devices by default.
const DefaultFilesystem = "ext4"

// defaultFilesystemMountOpts mount options to use for filesystem block devices by default.
const defaultFilesystemMountOptions = "discard"

// volIDQuotaSkip is used to indicate to drivers that quotas should not be setup, used during backup import.
const volIDQuotaSkip = int64(-1)

// VolumeType represents a storage volume type.
type VolumeType string

// VolumeTypeImage represents an image storage volume.
const VolumeTypeImage = VolumeType("images")

// VolumeTypeCustom represents a custom storage volume.
const VolumeTypeCustom = VolumeType("custom")

// VolumeTypeContainer represents a container storage volume.
const VolumeTypeContainer = VolumeType("containers")

// VolumeTypeVM represents a virtual-machine storage volume.
const VolumeTypeVM = VolumeType("virtual-machines")

// ContentType indicates the format of the volume.
type ContentType string

// ContentTypeFS indicates the volume will be populated with a mountabble filesystem.
const ContentTypeFS = ContentType("filesystem")

// ContentTypeBlock indicates the volume will be a block device and its contents and we do not
// know which filesystem(s) (if any) are in use.
const ContentTypeBlock = ContentType("block")

// BaseDirectories maps volume types to the expected directories.
var BaseDirectories = map[VolumeType][]string{
	VolumeTypeContainer: {"containers", "containers-snapshots"},
	VolumeTypeCustom:    {"custom", "custom-snapshots", "custom-backups"},
	VolumeTypeImage:     {"images"},
	VolumeTypeVM:        {"virtual-machines", "virtual-machines-snapshots"},
}

// Volume represents a storage volume, and provides functions to mount and unmount it.
type Volume struct {
	name              string
	pool              string
	poolConfig        map[string]string
	volType           VolumeType
	contentType       ContentType
	config            map[string]string
	driver            Driver
	customMountPath   string
	allowUnsafeResize bool // Whether to allow potentially destructive unchecked resizing of volume.
}

// NewVolume instantiates a new Volume struct.
func NewVolume(driver Driver, poolName string, volType VolumeType, contentType ContentType, volName string, volConfig, poolConfig map[string]string) Volume {
	return Volume{
		name:        volName,
		pool:        poolName,
		poolConfig:  poolConfig,
		volType:     volType,
		contentType: contentType,
		config:      volConfig,
		driver:      driver,
	}
}

// Name returns volume's name.
func (v Volume) Name() string {
	return v.name
}

// Config returns the volumes (unexpanded) config.
func (v Volume) Config() map[string]string {
	return v.config
}

// ExpandedConfig returns either the value of the volume's config key or the pool's config "volume.{key}" value.
func (v Volume) ExpandedConfig(key string) string {
	volVal, ok := v.config[key]
	if ok {
		return volVal
	}

	return v.poolConfig[fmt.Sprintf("volume.%s", key)]
}

// NewSnapshot instantiates a new Volume struct representing a snapshot of the parent volume.
func (v Volume) NewSnapshot(snapshotName string) (Volume, error) {
	if v.IsSnapshot() {
		return Volume{}, fmt.Errorf("Cannot create a snapshot volume from a snapshot")
	}

	fullSnapName := GetSnapshotVolumeName(v.name, snapshotName)
	return NewVolume(v.driver, v.pool, v.volType, v.contentType, fullSnapName, v.config, v.poolConfig), nil
}

// IsSnapshot indicates if volume is a snapshot.
func (v Volume) IsSnapshot() bool {
	return shared.IsSnapshot(v.name)
}

// MountPath returns the path where the volume will be mounted.
func (v Volume) MountPath() string {
	if v.customMountPath != "" {
		return v.customMountPath
	}

	return GetVolumeMountPath(v.pool, v.volType, v.name)
}

// EnsureMountPath creates the volume's mount path if missing, then sets the correct permission for the type.
// If permission setting fails and the volume is a snapshot then the error is ignored as snapshots are read only.
func (v Volume) EnsureMountPath() error {
	volPath := v.MountPath()

	revert := revert.New()
	defer revert.Fail()

	// Create volume's mount path if missing, with any created directories set to 0711.
	if !shared.PathExists(volPath) {
		err := os.Mkdir(volPath, 0711)
		if err != nil {
			return errors.Wrapf(err, "Failed to create mount directory %q", volPath)
		}
		revert.Add(func() { os.Remove(volPath) })
	}

	// Set very restrictive mode 0100 for non-custom and non-image volumes.
	mode := os.FileMode(0711)
	if v.volType != VolumeTypeCustom && v.volType != VolumeTypeImage {
		mode = os.FileMode(0100)
	}

	fInfo, err := os.Lstat(volPath)
	if err != nil {
		return errors.Wrapf(err, "Error getting mount directory info %q", volPath)
	}

	// We expect the mount path to be a directory, so use this for comparison.
	compareMode := os.ModeDir | mode

	// Set mode of actual volume's mount path if needed.
	if fInfo.Mode() != compareMode {
		err = os.Chmod(volPath, mode)

		// If the chmod failed, return the error as long as the volume is not a snapshot.
		// If the volume is a snapshot, we must ignore the error as snapshots are readonly and cannot be
		// modified after they are taken, such that any permission error is not fixable at mount time.
		if err != nil && !v.IsSnapshot() {
			return errors.Wrapf(err, "Failed to chmod mount directory %q (%04o)", volPath, mode)
		}
	}

	revert.Success()
	return nil
}

// MountTask runs the supplied task after mounting the volume if needed. If the volume was mounted
// for this then it is unmounted when the task finishes.
func (v Volume) MountTask(task func(mountPath string, op *operations.Operation) error, op *operations.Operation) error {
	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if v.IsSnapshot() {
		unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))

		ourMount, err := v.driver.MountVolumeSnapshot(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))
				v.driver.UnmountVolumeSnapshot(v, op)
				unlock()
			}()
		}
	} else {
		unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))

		ourMount, err := v.driver.MountVolume(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))
				v.driver.UnmountVolume(v, op)
				unlock()
			}()
		}
	}

	return task(v.MountPath(), op)
}

// UnmountTask runs the supplied task after unmounting the volume if needed. If the volume was unmounted
// for this then it is mounted when the task finishes.
func (v Volume) UnmountTask(task func(op *operations.Operation) error, op *operations.Operation) error {
	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if v.IsSnapshot() {
		unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))

		ourUnmount, err := v.driver.UnmountVolumeSnapshot(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourUnmount {
			defer func() {
				unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))
				v.driver.MountVolumeSnapshot(v, op)
				unlock()
			}()
		}
	} else {
		unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))

		ourUnmount, err := v.driver.UnmountVolume(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourUnmount {
			defer func() {
				unlock := locking.Lock(OperationLockName(v.pool, string(v.volType), v.name))
				v.driver.MountVolume(v, op)
				unlock()
			}()
		}
	}

	return task(op)
}

// Snapshots returns a list of snapshots for the volume.
func (v Volume) Snapshots(op *operations.Operation) ([]Volume, error) {
	if v.IsSnapshot() {
		return nil, fmt.Errorf("Volume is a snapshot")
	}

	snapshots, err := v.driver.VolumeSnapshots(v, op)
	if err != nil {
		return nil, err
	}

	snapVols := []Volume{}
	for _, snapName := range snapshots {
		snapshot, err := v.NewSnapshot(snapName)
		if err != nil {
			return nil, err
		}
		snapVols = append(snapVols, snapshot)
	}

	return snapVols, nil
}

// IsBlockBacked indicates whether storage device is block backed.
func (v Volume) IsBlockBacked() bool {
	return v.driver.Info().BlockBacking
}

// Type returns the volume type.
func (v Volume) Type() VolumeType {
	return v.volType
}

// ContentType returns the content type.
func (v Volume) ContentType() ContentType {
	return v.contentType
}

// IsVMBlock returns true if volume is a block volume for virtual machines or associated images.
func (v Volume) IsVMBlock() bool {
	return (v.volType == VolumeTypeVM || v.volType == VolumeTypeImage) && v.contentType == ContentTypeBlock
}

// NewVMBlockFilesystemVolume returns a copy of the volume with the content type set to ContentTypeFS and the
// config "size" property set to vmBlockFilesystemSize.
func (v Volume) NewVMBlockFilesystemVolume() Volume {
	// Copy volume config so modifications don't affect original volume.
	newConf := make(map[string]string, len(v.config))
	for k, v := range v.config {
		newConf[k] = v
	}

	// VM Block filesystems are a fixed size.
	newConf["size"] = vmBlockFilesystemSize

	return NewVolume(v.driver, v.pool, v.volType, ContentTypeFS, v.name, newConf, v.poolConfig)
}

// SetQuota calls SetVolumeQuota on the Volume's driver.
func (v Volume) SetQuota(size string, op *operations.Operation) error {
	return v.driver.SetVolumeQuota(v, size, op)
}

// SetConfigSize sets the size config property on the Volume (does not resize volume).
func (v Volume) SetConfigSize(size string) {
	v.config["size"] = size
}

// ConfigBlockFilesystem returns the filesystem to use for block volumes. Returns config value "block.filesystem"
// if defined in volume or pool's volume config, otherwise the DefaultFilesystem.
func (v Volume) ConfigBlockFilesystem() string {
	fs := v.ExpandedConfig("block.filesystem")
	if fs != "" {
		return fs
	}

	return DefaultFilesystem
}

// ConfigBlockMountOptions returns the filesystem mount options to use for block volumes. Returns config value
// "block.mount_options" if defined in volume or pool's volume config, otherwise defaultFilesystemMountOptions.
func (v Volume) ConfigBlockMountOptions() string {
	fs := v.ExpandedConfig("block.mount_options")
	if fs != "" {
		return fs
	}

	// Use some special options if the filesystem for the volume is BTRFS.
	if v.ConfigBlockFilesystem() == "btrfs" {
		return "user_subvol_rm_allowed,discard"
	}

	return defaultFilesystemMountOptions
}

// ConfigSize returns the size to use when creating new a volume. Returns config value "size" if defined in volume
// or pool's volume config, otherwise for block volumes and block-backed volumes the defaultBlockSize. For other
// volumes an empty string is returned if no size is defined.
func (v Volume) ConfigSize() string {
	size := v.ExpandedConfig("size")

	// If volume size isn't defined in either volume or pool config, then for block volumes or block-backed
	// volumes return the defaultBlockSize.
	if (size == "" || size == "0") && (v.contentType == ContentTypeBlock || v.driver.Info().BlockBacking) {
		return defaultBlockSize
	}

	// Return defined size or empty string if not defined.
	return size
}

// ConfigSizeFromSource derives the volume size to use for a new volume when copying from a source volume.
// Where possible (if the source volume has a volatile.rootfs.size property), it checks that the source volume
// isn't larger than the volume's "size" setting and the pool's "volume.size" setting.
func (v Volume) ConfigSizeFromSource(srcVol Volume) (string, error) {
	// If source is not an image, then only use volume specified size. This is so the pool volume size isn't
	// taken into account for non-image volume copies.
	if srcVol.volType != VolumeTypeImage {
		return v.config["size"], nil
	}

	// VM config filesystem volumes should always have a fixed specified size, so just return volume size.
	if v.volType == VolumeTypeVM && v.contentType == ContentTypeFS {
		return v.config["size"], nil
	}

	// If the source image doesn't have any size information, then use volume/pool/default size in that order.
	if srcVol.config["volatile.rootfs.size"] == "" {
		return v.ConfigSize(), nil
	}

	imgSizeBytes, err := units.ParseByteSizeString(srcVol.config["volatile.rootfs.size"])
	if err != nil {
		return "", err
	}

	// If volume/pool size is specified (excluding default size), then check it against the image minimum size.
	volSize := v.ExpandedConfig("size")
	if volSize != "" && volSize != "0" {
		volSizeBytes, err := units.ParseByteSizeString(volSize)
		if err != nil {
			return volSize, err
		}

		// The volume/pool specified size is smaller than image minimum size. We must not continue as
		// these specified sizes provide protection against unpacking a massive image and filling the pool.
		if volSizeBytes < imgSizeBytes {
			return "", fmt.Errorf("Source image size (%d) exceeds specified volume size (%d)", imgSizeBytes, volSizeBytes)
		}

		// Use the specified volume size.
		return volSize, nil
	}

	// If volume/pool size is not specified, then fallback to default volume size if relevant and compare.
	volSize = v.ConfigSize()
	if volSize != "" && volSize != "0" {
		volSizeBytes, err := units.ParseByteSizeString(volSize)
		if err != nil {
			return "", err
		}

		// Use image minimum size as volSize if the default volume size is smaller.
		if volSizeBytes < imgSizeBytes {
			return srcVol.config["volatile.rootfs.size"], nil
		}
	}

	// Use the default volume size.
	return volSize, nil
}
