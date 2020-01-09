package drivers

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/storage/locking"
	"github.com/lxc/lxd/shared"
)

var defaultBlockSize = "10GB"

// DefaultFilesystem filesytem to use for block devices by default.
var DefaultFilesystem = "ext4"

var volIDQuotaSkip = int64(-1)

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
const ContentTypeFS = ContentType("fs")

// ContentTypeBlock indicates the volume will be a block device and its contents and we do not
// know which filesystem(s) (if any) are in use.
const ContentTypeBlock = ContentType("block")

// BaseDirectories maps volume types to the expected directories.
var BaseDirectories = map[VolumeType][]string{
	VolumeTypeContainer: {"containers", "containers-snapshots"},
	VolumeTypeCustom:    {"custom", "custom-snapshots"},
	VolumeTypeImage:     {"images"},
	VolumeTypeVM:        {"virtual-machines", "virtual-machines-snapshots"},
}

// Volume represents a storage volume, and provides functions to mount and unmount it.
type Volume struct {
	name        string
	pool        string
	poolConfig  map[string]string
	volType     VolumeType
	contentType ContentType
	config      map[string]string
	driver      Driver
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
	return GetVolumeMountPath(v.pool, v.volType, v.name)
}

// EnsureMountPath creates the volume's mount path if missing, then sets the correct permission for the type.
func (v Volume) EnsureMountPath() error {
	volPath := v.MountPath()

	// Create volume's mount path, with any created directories set to 0711.
	err := os.Mkdir(volPath, 0711)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Set very restrictive mode 0100 for non-custom and non-image volumes.
	mode := os.FileMode(0711)
	if v.volType != VolumeTypeCustom && v.volType != VolumeTypeImage {
		mode = os.FileMode(0100)
	}

	// Set mode of actual volume's mount path.
	err = os.Chmod(volPath, mode)
	if err != nil {
		return err
	}

	return nil
}

// MountTask runs the supplied task after mounting the volume if needed. If the volume was mounted
// for this then it is unmounted when the task finishes.
func (v Volume) MountTask(task func(mountPath string, op *operations.Operation) error, op *operations.Operation) error {
	isSnap := v.IsSnapshot()

	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if isSnap {
		unlock := locking.Lock(v.pool, string(v.volType), v.name)

		ourMount, err := v.driver.MountVolumeSnapshot(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := locking.Lock(v.pool, string(v.volType), v.name)
				v.driver.UnmountVolumeSnapshot(v, op)
				unlock()
			}()
		}
	} else {
		unlock := locking.Lock(v.pool, string(v.volType), v.name)

		ourMount, err := v.driver.MountVolume(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := locking.Lock(v.pool, string(v.volType), v.name)
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
	isSnap := v.IsSnapshot()

	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if isSnap {
		unlock := locking.Lock(v.pool, string(v.volType), v.name)

		ourUnmount, err := v.driver.UnmountVolumeSnapshot(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourUnmount {
			defer func() {
				unlock := locking.Lock(v.pool, string(v.volType), v.name)
				v.driver.MountVolumeSnapshot(v, op)
				unlock()
			}()
		}
	} else {
		unlock := locking.Lock(v.pool, string(v.volType), v.name)

		ourUnmount, err := v.driver.UnmountVolume(v, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourUnmount {
			defer func() {
				unlock := locking.Lock(v.pool, string(v.volType), v.name)
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
