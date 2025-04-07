package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/archive"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
	"github.com/flosch/pongo2"
)

const defaultSnapshotPattern = "snap%d"

// ConfigDiff returns a diff of the provided configs. Additionally, it returns whether or not
// only user properties have been changed.
func ConfigDiff(oldConfig map[string]string, newConfig map[string]string) ([]string, bool) {
	changedConfig := []string{}
	userOnly := true
	for key := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Skip on no change
	if len(changedConfig) == 0 {
		return nil, false
	}

	return changedConfig, userOnly
}

// VolumeTypeToDBType converts volume type to internal volume type DB code.
func VolumeTypeToDBType(volType drivers.VolumeType) (cluster.StoragePoolVolumeType, error) {
	switch volType {
	case drivers.VolumeTypeContainer:
		return cluster.StoragePoolVolumeTypeContainer, nil
	case drivers.VolumeTypeVM:
		return cluster.StoragePoolVolumeTypeVM, nil
	case drivers.VolumeTypeImage:
		return cluster.StoragePoolVolumeTypeImage, nil
	case drivers.VolumeTypeCustom:
		return cluster.StoragePoolVolumeTypeCustom, nil
	}

	return -1, fmt.Errorf("Invalid storage volume type: %q", volType)
}

// VolumeDBTypeToType converts internal volume type DB code to storage driver volume type.
func VolumeDBTypeToType(volDBType cluster.StoragePoolVolumeType) drivers.VolumeType {
	switch volDBType {
	case cluster.StoragePoolVolumeTypeContainer:
		return drivers.VolumeTypeContainer
	case cluster.StoragePoolVolumeTypeVM:
		return drivers.VolumeTypeVM
	case cluster.StoragePoolVolumeTypeImage:
		return drivers.VolumeTypeImage
	case cluster.StoragePoolVolumeTypeCustom:
		return drivers.VolumeTypeCustom
	}

	return drivers.VolumeTypeCustom
}

// InstanceTypeToVolumeType converts instance type to storage driver volume type.
func InstanceTypeToVolumeType(instType instancetype.Type) (drivers.VolumeType, error) {
	switch instType {
	case instancetype.Container:
		return drivers.VolumeTypeContainer, nil
	case instancetype.VM:
		return drivers.VolumeTypeVM, nil
	}

	return "", fmt.Errorf("Invalid instance type")
}

// VolumeTypeToAPIInstanceType converts storage driver volume type to API instance type type.
func VolumeTypeToAPIInstanceType(volType drivers.VolumeType) (api.InstanceType, error) {
	switch volType {
	case drivers.VolumeTypeContainer:
		return api.InstanceTypeContainer, nil
	case drivers.VolumeTypeVM:
		return api.InstanceTypeVM, nil
	}

	return api.InstanceTypeAny, fmt.Errorf("Volume type doesn't have equivalent instance type")
}

// VolumeContentTypeToDBContentType converts volume type to internal code.
func VolumeContentTypeToDBContentType(contentType drivers.ContentType) (cluster.StoragePoolVolumeContentType, error) {
	switch contentType {
	case drivers.ContentTypeBlock:
		return cluster.StoragePoolVolumeContentTypeBlock, nil
	case drivers.ContentTypeFS:
		return cluster.StoragePoolVolumeContentTypeFS, nil
	case drivers.ContentTypeISO:
		return cluster.StoragePoolVolumeContentTypeISO, nil
	}

	return -1, fmt.Errorf("Invalid volume content type")
}

// VolumeDBContentTypeToContentType converts internal content type DB code to driver representation.
func VolumeDBContentTypeToContentType(volDBType cluster.StoragePoolVolumeContentType) drivers.ContentType {
	switch volDBType {
	case cluster.StoragePoolVolumeContentTypeBlock:
		return drivers.ContentTypeBlock
	case cluster.StoragePoolVolumeContentTypeFS:
		return drivers.ContentTypeFS
	case cluster.StoragePoolVolumeContentTypeISO:
		return drivers.ContentTypeISO
	}

	return drivers.ContentTypeFS
}

// VolumeDBGet loads a volume from the database.
func VolumeDBGet(pool Pool, projectName string, volumeName string, volumeType drivers.VolumeType) (*db.StorageVolume, error) {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a lxdBackend")
	}

	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return nil, err
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volDBType, volumeName, true)
		if err != nil {
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Storage volume %q in project %q of type %q does not exist on pool %q: %w", volumeName, projectName, volumeType, pool.Name(), err)
			}

			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return dbVolume, nil
}

// VolumeDBCreate creates a volume in the database.
// If volumeConfig is supplied, it is modified with any driver level default config options (if not set).
// If removeUnknownKeys is true, any unknown config keys are removed from volumeConfig rather than failing.
func VolumeDBCreate(pool Pool, projectName string, volumeName string, volumeDescription string, volumeType drivers.VolumeType, snapshot bool, volumeConfig map[string]string, creationDate time.Time, expiryDate time.Time, contentType drivers.ContentType, removeUnknownKeys bool, hasSource bool) error {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return fmt.Errorf("Pool is not a lxdBackend")
	}

	// Prevent using this function to create storage volume bucket records.
	if volumeType == drivers.VolumeTypeBucket {
		return fmt.Errorf("Cannot store volume using bucket type")
	}

	// If the volumeType represents an instance type then check that the volumeConfig doesn't contain any of
	// the instance disk effective override fields (which should not be stored in the database).
	if volumeType.IsInstance() {
		for _, k := range instanceDiskVolumeEffectiveFields {
			_, found := volumeConfig[k]
			if found {
				return fmt.Errorf("Instance disk effective override field %q should not be stored in volume config", k)
			}
		}
	}

	// Convert the volume type to our internal integer representation.
	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return err
	}

	volDBContentType, err := VolumeContentTypeToDBContentType(contentType)
	if err != nil {
		return err
	}

	// Make sure that we don't pass a nil to the next function.
	if volumeConfig == nil {
		volumeConfig = map[string]string{}
	}

	volType := VolumeDBTypeToType(volDBType)
	vol := drivers.NewVolume(pool.Driver(), pool.Name(), volType, contentType, volumeName, volumeConfig, pool.Driver().Config())

	// Set source indicator.
	vol.SetHasSource(hasSource)

	// Fill default config.
	err = pool.Driver().FillVolumeConfig(vol)
	if err != nil {
		return err
	}

	// Validate config.
	err = pool.Driver().ValidateVolume(vol, removeUnknownKeys)
	if err != nil {
		return err
	}

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry for the storage volume.
		if snapshot {
			_, err = tx.CreateStorageVolumeSnapshot(ctx, projectName, volumeName, volumeDescription, volDBType, pool.ID(), vol.Config(), creationDate, expiryDate)
		} else {
			_, err = tx.CreateStoragePoolVolume(ctx, projectName, volumeName, volumeDescription, volDBType, pool.ID(), vol.Config(), volDBContentType, creationDate)
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("Error inserting volume %q for project %q in pool %q of type %q into database %q", volumeName, projectName, pool.Name(), volumeType, err)
	}

	return nil
}

// VolumeDBDelete deletes a volume from the database.
func VolumeDBDelete(pool Pool, projectName string, volumeName string, volumeType drivers.VolumeType) error {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return fmt.Errorf("Pool is not a lxdBackend")
	}

	// Convert the volume type to our internal integer representation.
	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return err
	}

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RemoveStoragePoolVolume(ctx, projectName, volumeName, volDBType, pool.ID())
	})
	if err != nil && !response.IsNotFoundError(err) {
		return fmt.Errorf("Error deleting storage volume from database: %w", err)
	}

	return nil
}

// VolumeDBSnapshotsGet loads a list of snapshots volumes from the database.
func VolumeDBSnapshotsGet(pool Pool, projectName string, volume string, volumeType drivers.VolumeType) ([]db.StorageVolumeArgs, error) {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a lxdBackend")
	}

	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return nil, err
	}

	var snapshots []db.StorageVolumeArgs

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		snapshots, err = tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, projectName, volume, volDBType, pool.ID())

		return err
	})
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// BucketDBGet loads a bucket from the database.
func BucketDBGet(pool Pool, projectName string, bucketName string, memberSpecific bool) (*db.StorageBucket, error) {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a lxdBackend")
	}

	var err error
	var bucket *db.StorageBucket

	// Get the storage bucket.
	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), projectName, memberSpecific, bucketName)
		if err != nil {
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Storage bucket %q in project %q does not exist on pool %q: %w", bucketName, projectName, pool.Name(), err)
			}

			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return bucket, nil
}

// BucketDBCreate creates a bucket in the database.
// The supplied bucket's config may be modified with defaults for the storage pool being used.
// Returns bucket DB record ID.
func BucketDBCreate(ctx context.Context, pool Pool, projectName string, memberSpecific bool, bucket *api.StorageBucketsPost) (int64, error) {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return -1, fmt.Errorf("Pool is not a lxdBackend")
	}

	// Make sure that we don't pass a nil to the next function.
	if bucket.Config == nil {
		bucket.Config = map[string]string{}
	}

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := drivers.NewVolume(pool.Driver(), pool.Name(), drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config, pool.Driver().Config())

	// Fill default config.
	err := pool.Driver().FillVolumeConfig(bucketVol)
	if err != nil {
		return -1, err
	}

	// Validate bucket name.
	err = pool.Driver().ValidateBucket(bucketVol)
	if err != nil {
		return -1, err
	}

	// Validate bucket volume config.
	err = pool.Driver().ValidateVolume(bucketVol, false)
	if err != nil {
		return -1, err
	}

	var bucketID int64

	err = p.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry for the storage bucket.
		bucketID, err = tx.CreateStoragePoolBucket(ctx, p.ID(), projectName, memberSpecific, *bucket)

		return err
	})
	if err != nil {
		return -1, fmt.Errorf("Failed inserting storage bucket %q for project %q in pool %q into database: %w", bucket.Name, projectName, pool.Name(), err)
	}

	return bucketID, nil
}

// BucketDBDelete deletes a bucket from the database.
func BucketDBDelete(ctx context.Context, pool Pool, bucketID int64) error {
	p, ok := pool.(*lxdBackend)
	if !ok {
		return fmt.Errorf("Pool is not a lxdBackend")
	}

	err := p.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteStoragePoolBucket(ctx, p.ID(), bucketID)
	})
	if err != nil && !response.IsNotFoundError(err) {
		return fmt.Errorf("Failed deleting storage bucket from database: %w", err)
	}

	return nil
}

// poolAndVolumeCommonRules returns a map of pool and volume config common rules common to all drivers.
// When vol argument is nil function returns pool specific rules.
func poolAndVolumeCommonRules(vol *drivers.Volume) map[string]func(string) error {
	rules := map[string]func(string) error{
		// Note: size should not be modifiable for non-custom volumes and should be checked
		// in the relevant volume update functions.

		// lxdmeta:generate(entities=storage-btrfs,storage-lvm,storage-zfs; group=pool-conf; key=size)
		//
		// When creating loop-based pools, specify the size in bytes ({ref}`suffixes <instances-limit-units>` are supported).
		// You can increase the size to grow the storage pool.
		//
		// The default (`auto`) creates a storage pool that uses 20% of the free disk space,
		// with a minimum of 5 GiB and a maximum of 30 GiB.
		// ---
		//  type: string
		//  defaultdesc: auto (20% of free disk space, >= 5 GiB and <= 30 GiB)
		//  shortdesc: Size of the storage pool (for loop-based pools)
		//  scope: local

		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs; group=volume-conf; key=size)
		//
		// ---
		//  type: string
		//  condition: appropriate driver
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global

		// lxdmeta:generate(entities=storage-cephobject; group=bucket-conf; key=size)
		//
		// ---
		//  type: string
		//  shortdesc: Quota of the storage bucket
		//  scope: local

		// lxdmeta:generate(entities=storage-btrfs,storage-lvm,storage-zfs; group=bucket-conf; key=size)
		//
		// ---
		//  type: string
		//  condition: appropriate driver
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage bucket
		//  scope: local
		"size": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex,storage-pure; group=volume-conf; key=snapshots.expiry)
		// Specify an expression like `1M 2H 3d 4w 5m 6y`.
		// ---
		//  type: string
		//  condition: custom volume
		//  defaultdesc: same as `volume.snapshots.expiry`
		//  shortdesc: When snapshots are to be deleted
		//  scope: global
		"snapshots.expiry": func(value string) error {
			// Validate expression
			_, err := shared.GetExpiry(time.Time{}, value)
			return err
		},
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex,storage-pure; group=volume-conf; key=snapshots.schedule)
		// Specify either a cron expression (`<minute> <hour> <dom> <month> <dow>`), a comma-separated list of schedule aliases (`@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@annually`, `@yearly`), or leave empty to disable automatic snapshots (the default).
		// ---
		//  type: string
		//  condition: custom volume
		//  defaultdesc: same as `snapshots.schedule`
		//  shortdesc: Schedule for automatic volume snapshots
		//  scope: global
		"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly"})),
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex,storage-pure; group=volume-conf; key=snapshots.pattern)
		// You can specify a naming template that is used for scheduled snapshots and unnamed snapshots.
		//
		// {{snapshot_pattern_detail}}
		// ---
		//  type: string
		//  condition: custom volume
		//  defaultdesc: same as `volume.snapshots.pattern` or `snap%d`
		//  shortdesc: Template for the snapshot name
		//  scope: global
		"snapshots.pattern": validate.IsAny,
	}

	// security.shifted and security.unmapped are only relevant for custom filesystem volumes.
	if vol == nil || (vol.Type() == drivers.VolumeTypeCustom && vol.ContentType() == drivers.ContentTypeFS) {
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex; group=volume-conf; key=security.shifted)
		// Enabling this option allows attaching the volume to multiple isolated instances.
		// ---
		//  type: bool
		//  condition: custom volume
		//  defaultdesc: same as `volume.security.shifted` or `false`
		//  shortdesc: Enable ID shifting overlay
		//  scope: global
		rules["security.shifted"] = validate.Optional(validate.IsBool)
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex; group=volume-conf; key=security.unmapped)
		//
		// ---
		//  type: bool
		//  condition: custom volume
		//  defaultdesc: same as `volume.security.unmappped` or `false`
		//  shortdesc: Disable ID mapping for the volume
		//  scope: global
		rules["security.unmapped"] = validate.Optional(validate.IsBool)
	}

	// security.shared guards virtual-machine and custom block volumes.
	if vol == nil || ((vol.Type() == drivers.VolumeTypeCustom || vol.Type() == drivers.VolumeTypeVM) && vol.ContentType() == drivers.ContentTypeBlock) {
		// lxdmeta:generate(entities=storage-btrfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex; group=volume-conf; key=security.shared)
		// Enabling this option allows sharing the volume across multiple instances despite the possibility of data loss.
		//
		// ---
		//  type: bool
		//  condition: virtual-machine or custom block volume
		//  defaultdesc: same as `volume.security.shared` or `false`
		//  shortdesc: Enable volume sharing
		//  scope: global
		rules["security.shared"] = validate.Optional(validate.IsBool)
	}

	// Those keys are only valid for volumes.
	if vol != nil {
		// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex,storage-pure; group=volume-conf; key=volatile.uuid)
		//
		// ---
		//  type: string
		//  defaultdesc: random UUID
		//  shortdesc: The volume's UUID
		//  scope: global
		rules["volatile.uuid"] = validate.Optional(validate.IsUUID)
	}

	return rules
}

// validatePoolCommonRules returns a map of pool config rules common to all drivers.
func validatePoolCommonRules() map[string]func(string) error {
	rules := map[string]func(string) error{
		// lxdmeta:generate(entities=storage-btrfs; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Path to an existing block device, loop file, or Btrfs subvolume
		//  scope: local

		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Existing CephFS file system or file system path to use
		//  scope: local

		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Existing OSD storage pool to use
		//  scope: local

		// lxdmeta:generate(entities=storage-dir; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Path to an existing directory
		//  scope: local

		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Path to an existing block device, loop file, or LVM volume group
		//  scope: local

		// lxdmeta:generate(entities=storage-zfs; group=pool-conf; key=source)
		//
		// ---
		//  type: string
		//  shortdesc: Path to an existing block device, loop file, or ZFS dataset/pool
		//  scope: local
		"source": validate.IsAny,
		// lxdmeta:generate(entities=storage-btrfs,storage-lvm,storage-zfs; group=pool-conf; key=source.wipe)
		// Set this option to `true` to wipe the block device specified in `source`
		// prior to creating the storage pool.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to wipe the block device before creating the pool
		//  scope: local
		"source.wipe":             validate.Optional(validate.IsBool),
		"volatile.initial_source": validate.IsAny,
		// lxdmeta:generate(entities=storage-dir,storage-lvm,storage-powerflex; group=pool-conf; key=rsync.bwlimit)
		// When `rsync` must be used to transfer storage entities, this option specifies the upper limit
		// to be placed on the socket I/O.
		// ---
		//  type: string
		//  defaultdesc: `0` (no limit)
		//  shortdesc: Upper limit on the socket I/O for `rsync`
		//  scope: global
		"rsync.bwlimit": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-dir,storage-lvm,storage-powerflex; group=pool-conf; key=rsync.compression)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to use compression while migrating storage pools
		//  scope: global
		"rsync.compression": validate.Optional(validate.IsBool),
	}

	// Add to pool config rules (prefixed with volume.*) which are common for pool and volume.
	for volRule, volValidator := range poolAndVolumeCommonRules(nil) {
		rules["volume."+volRule] = volValidator
	}

	return rules
}

// validateVolumeCommonRules returns a map of volume config rules common to all drivers.
func validateVolumeCommonRules(vol drivers.Volume) map[string]func(string) error {
	rules := poolAndVolumeCommonRules(&vol)

	// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex; group=volume-conf; key=volatile.idmap.last)
	//
	// ---
	//   type: string
	//   shortdesc: JSON-serialized UID/GID map that has been applied to the volume
	//   condition: filesystem

	// lxdmeta:generate(entities=storage-btrfs,storage-cephfs,storage-ceph,storage-dir,storage-lvm,storage-zfs,storage-powerflex; group=volume-conf; key=volatile.idmap.next)
	//
	// ---
	//   type: string
	//   shortdesc: JSON-serialized UID/GID map that has been applied to the volume
	//   condition: filesystem
	if vol.ContentType() == drivers.ContentTypeFS {
		rules["volatile.idmap.last"] = validate.IsAny
		rules["volatile.idmap.next"] = validate.IsAny
	}

	// block.mount_options and block.filesystem settings are only relevant for drivers that are block backed
	// and when there is a filesystem to actually mount. This includes filesystem volumes and VM Block volumes,
	// as they have an associated config filesystem volume that shares the config.
	if vol.IsBlockBacked() && (vol.ContentType() == drivers.ContentTypeFS || vol.IsVMBlock()) {
		rules["block.mount_options"] = validate.IsAny

		// Note: block.filesystem should not be modifiable after volume created.
		// This should be checked in the relevant volume update functions.
		rules["block.filesystem"] = validate.IsAny
	}

	// volatile.rootfs.size is only used for image volumes.
	if vol.Type() == drivers.VolumeTypeImage {
		rules["volatile.rootfs.size"] = validate.Optional(validate.IsInt64)
	}

	return rules
}

// ImageUnpack unpacks a filesystem image into the destination path.
// There are several formats that images can come in:
// Container Format A: Separate metadata tarball and root squashfs file.
//   - Unpack metadata tarball into mountPath.
//   - Unpack root squashfs file into mountPath/rootfs.
//
// Container Format B: Combined tarball containing metadata files and root squashfs.
//   - Unpack combined tarball into mountPath.
//
// VM Format A: Separate metadata tarball and root qcow2 file.
//   - Unpack metadata tarball into mountPath.
//   - Check rootBlockPath is a file and convert qcow2 file into raw format in rootBlockPath.
func ImageUnpack(imageFile string, vol drivers.Volume, destBlockFile string, sysOS *sys.OS, allowUnsafeResize bool, tracker *ioprogress.ProgressTracker) (int64, error) {
	l := logger.Log.AddContext(logger.Ctx{"imageFile": imageFile, "volName": vol.Name()})
	l.Info("Image unpack started")
	defer l.Info("Image unpack stopped")

	// For all formats, first unpack the metadata (or combined) tarball into destPath.
	imageRootfsFile := imageFile + ".rootfs"
	destPath := vol.MountPath()

	// If no destBlockFile supplied then this is a container image unpack.
	if destBlockFile == "" {
		rootfsPath := filepath.Join(destPath, "rootfs")

		// Unpack the main image file.
		err := archive.Unpack(imageFile, destPath, vol.IsBlockBacked(), sysOS, tracker)
		if err != nil {
			return -1, err
		}

		// Check for separate root file.
		if shared.PathExists(imageRootfsFile) {
			err = os.MkdirAll(rootfsPath, 0755)
			if err != nil {
				return -1, fmt.Errorf("Error creating rootfs directory")
			}

			err = archive.Unpack(imageRootfsFile, rootfsPath, vol.IsBlockBacked(), sysOS, tracker)
			if err != nil {
				return -1, err
			}
		}

		// Check that the container image unpack has resulted in a rootfs dir.
		if !shared.PathExists(rootfsPath) {
			return -1, fmt.Errorf("Image is missing a rootfs: %s", imageFile)
		}

		// Done with this.
		return 0, nil
	}

	// If a rootBlockPath is supplied then this is a VM image unpack.

	// Validate the target.
	fileInfo, err := os.Stat(destBlockFile)
	if err != nil && !os.IsNotExist(err) {
		return -1, err
	}

	if fileInfo != nil && fileInfo.IsDir() {
		// If the dest block file exists, and it is a directory, fail.
		return -1, fmt.Errorf("Root block path isn't a file: %s", destBlockFile)
	}

	// convertBlockImage converts the qcow2 block image file into a raw block device. If needed it will attempt
	// to enlarge the destination volume to accommodate the unpacked qcow2 image file.
	convertBlockImage := func(imgPath string, dstPath string, tracker *ioprogress.ProgressTracker) (int64, error) {
		imgFormat, imgVirtualSize, err := qemuImageInfo(sysOS, imgPath, tracker)
		if err != nil {
			return -1, err
		}

		// Belt and braces qcow2 check.
		if imgFormat != "qcow2" {
			return -1, fmt.Errorf("Unexpected image format %q", imgFormat)
		}

		// Check whether image is allowed to be unpacked into pool volume. Create a partial image volume
		// struct and then use it to check that target volume size can be set as needed.
		imgVolConfig := map[string]string{
			"volatile.rootfs.size": fmt.Sprint(imgVirtualSize),
		}

		imgVol := drivers.NewVolume(nil, "", drivers.VolumeTypeImage, drivers.ContentTypeBlock, "", imgVolConfig, nil)

		l.Debug("Checking image unpack size")
		newVolSize, err := vol.ConfigSizeFromSource(imgVol)
		if err != nil {
			return -1, err
		}

		if shared.PathExists(dstPath) {
			volSizeBytes, err := block.DiskSizeBytes(dstPath)
			if err != nil {
				return -1, fmt.Errorf("Error getting current size of %q: %w", dstPath, err)
			}

			// If the target volume's size is smaller than the image unpack size, then we need to
			// increase the target volume's size.
			if volSizeBytes < imgVirtualSize {
				l.Debug("Increasing volume size", logger.Ctx{"imgPath": imgPath, "dstPath": dstPath, "oldSize": volSizeBytes, "newSize": newVolSize, "allowUnsafeResize": allowUnsafeResize})
				err = vol.SetQuota(newVolSize, allowUnsafeResize, nil)
				if err != nil {
					return -1, fmt.Errorf("Error increasing volume size: %w", err)
				}
			}
		}

		// Convert the qcow2 format to a raw block device.
		l.Debug("Converting qcow2 image to raw disk", logger.Ctx{"imgPath": imgPath, "dstPath": dstPath})

		cmd := []string{
			"nice", "-n19", // Run with low priority to reduce CPU impact on other processes.
			"qemu-img", "convert", "-p", "-f", "qcow2", "-O", "raw", "-t", "writeback",
		}

		// Check for Direct I/O support.
		from, err := os.OpenFile(imgPath, unix.O_DIRECT|unix.O_RDONLY, 0)
		if err == nil {
			cmd = append(cmd, "-T", "none")
			_ = from.Close()
		}

		to, err := os.OpenFile(dstPath, unix.O_DIRECT|unix.O_WRONLY, 0)
		if err == nil {
			cmd = append(cmd, "-t", "none")
			_ = to.Close()
		}

		// Extra options when dealing with block devices.
		if shared.IsBlockdevPath(dstPath) {
			// Parallel unpacking.
			cmd = append(cmd, "-W")

			// Our block devices are clean, so skip zeroes.
			cmd = append(cmd, "-n", "--target-is-zero")
		}

		cmd = append(cmd, imgPath, dstPath)

		_, err = apparmor.QemuImg(sysOS, cmd, imgPath, dstPath, tracker)
		if err != nil {
			return -1, fmt.Errorf("Failed converting image to raw at %q: %w", dstPath, err)
		}

		return imgVirtualSize, nil
	}

	var imgSize int64

	if shared.PathExists(imageRootfsFile) {
		// Unpack the main image file.
		err := archive.Unpack(imageFile, destPath, vol.IsBlockBacked(), sysOS, tracker)
		if err != nil {
			return -1, err
		}

		// Convert the qcow2 format to a raw block device.
		imgSize, err = convertBlockImage(imageRootfsFile, destBlockFile, tracker)
		if err != nil {
			return -1, err
		}
	} else {
		// Dealing with unified tarballs require an initial unpack to a temporary directory.
		tempDir, err := os.MkdirTemp(shared.VarPath("images"), "lxd_image_unpack_")
		if err != nil {
			return -1, err
		}

		defer func() { _ = os.RemoveAll(tempDir) }()

		// Unpack the whole image.
		err = archive.Unpack(imageFile, tempDir, vol.IsBlockBacked(), sysOS, tracker)
		if err != nil {
			return -1, err
		}

		imgPath := filepath.Join(tempDir, "rootfs.img")

		// Convert the qcow2 format to a raw block device.
		imgSize, err = convertBlockImage(imgPath, destBlockFile, tracker)
		if err != nil {
			return -1, err
		}

		// Delete the qcow2.
		err = os.Remove(imgPath)
		if err != nil {
			return -1, fmt.Errorf("Failed to remove %q: %w", imgPath, err)
		}

		// Transfer the content excluding the destBlockFile name so that we don't delete the block file
		// created above if the storage driver stores image files in the same directory as destPath.
		_, err = rsync.LocalCopy(tempDir, destPath, "", true, "--exclude", filepath.Base(destBlockFile))
		if err != nil {
			return -1, err
		}
	}

	return imgSize, nil
}

// qemuImageInfo retrieves the format and virtual size of an image (size after unpacking the image)
// on the given path.
func qemuImageInfo(sysOS *sys.OS, imagePath string, tracker *ioprogress.ProgressTracker) (format string, bytes int64, err error) {
	cmd := []string{
		// Use prlimit because qemu-img can consume considerable RAM & CPU time if fed
		// a maliciously crafted disk image. Since cloud tenants are not to be trusted,
		// ensure QEMU is limited to 1 GiB address space and 2 seconds of CPU time.
		// This should be more than enough for real world images.
		"prlimit", "--cpu=2", "--as=1073741824",
		"qemu-img", "info", imagePath, "--output", "json",
	}

	out, err := apparmor.QemuImg(sysOS, cmd, imagePath, "", tracker)
	if err != nil {
		return "", -1, fmt.Errorf("qemu-img info: %v", err)
	}

	imgInfo := struct {
		Format      string `json:"format"`
		VirtualSize int64  `json:"virtual-size"` // Image size after unpacking.
	}{}

	err = json.Unmarshal([]byte(out), &imgInfo)
	if err != nil {
		return "", -1, fmt.Errorf("Failed unmarshalling image info: %v", err)
	}

	return imgInfo.Format, imgInfo.VirtualSize, nil
}

// InstanceContentType returns the instance's content type.
func InstanceContentType(inst instance.Instance) drivers.ContentType {
	contentType := drivers.ContentTypeFS
	if inst.Type() == instancetype.VM {
		contentType = drivers.ContentTypeBlock
	}

	return contentType
}

// volumeIsUsedByDevice; true when vol is referred to by dev
// inst is the instance dev belongs to, or nil if dev is part of a profile.
func volumeIsUsedByDevice(vol api.StorageVolume, inst *db.InstanceArgs, dev map[string]string) (bool, error) {
	if dev["type"] != cluster.TypeDisk.String() {
		return false, nil
	}

	if dev["pool"] != vol.Pool {
		return false, nil
	}

	if inst != nil && instancetype.IsRootDiskDevice(dev) {
		rootVolumeType, err := InstanceTypeToVolumeType(inst.Type)
		if err != nil {
			return false, err
		}

		rootVolumeDBType, err := VolumeTypeToDBType(rootVolumeType)
		if err != nil {
			return false, err
		}

		if inst.Name == vol.Name && rootVolumeDBType.String() == vol.Type {
			return true, nil
		}
	}

	var volName string
	var snapName string

	if shared.IsSnapshot(vol.Name) {
		parts := strings.SplitN(vol.Name, shared.SnapshotDelimiter, 2)
		volName, snapName = parts[0], parts[1]
	} else if dev["source.snapshot"] != "" {
		// vol is not a snapshot but dev refers to one
		return false, nil
	} else {
		volName = vol.Name
	}

	volumeTypeName := cluster.StoragePoolVolumeTypeNameCustom
	if dev["source.type"] != "" {
		volumeTypeName = dev["source.type"]
	}

	if volumeTypeName == vol.Type && dev["source"] == volName && dev["source.snapshot"] == snapName {
		return true, nil
	}

	return false, nil
}

// VolumeUsedByProfileDevices finds profiles using a volume and passes them to profileFunc for evaluation.
// The profileFunc is provided with a profile config, project config and a list of device names that are using
// the volume.
func VolumeUsedByProfileDevices(s *state.State, poolName string, projectName string, vol *api.StorageVolume, profileFunc func(profileID int64, profile api.Profile, project api.Project, usedByDevices []string) error) error {
	// Convert the volume type name to our internal integer representation.
	volumeType, err := cluster.StoragePoolVolumeTypeFromName(vol.Type)
	if err != nil {
		return err
	}

	var profiles []api.Profile
	var profileIDs []int64
	var profileProjects []*api.Project
	// Retrieve required info from the database in single transaction for performance.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading projects: %w", err)
		}

		// Index of all projects by name.
		projectMap := make(map[string]*api.Project, len(projects))
		for _, project := range projects {
			projectMap[project.Name], err = project.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading config for projec %q: %w", project.Name, err)
			}
		}

		dbProfiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading profiles: %w", err)
		}

		// Get all the profile configs.
		profileConfigs, err := cluster.GetConfig(ctx, tx.Tx(), "profile")
		if err != nil {
			return fmt.Errorf("Failed loading profile configs: %w", err)
		}

		// Get all the profile devices.
		profileDevices, err := cluster.GetDevices(ctx, tx.Tx(), "profile")
		if err != nil {
			return fmt.Errorf("Failed loading profile devices: %w", err)
		}

		for _, profile := range dbProfiles {
			apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
			if err != nil {
				return fmt.Errorf("Failed getting API Profile %q: %w", profile.Name, err)
			}

			profileIDs = append(profileIDs, int64(profile.ID))
			profiles = append(profiles, *apiProfile)
		}

		profileProjects = make([]*api.Project, len(dbProfiles))
		for i, p := range dbProfiles {
			profileProjects[i] = projectMap[p.Project]
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Iterate all profiles, consider only those which belong to a project that has the same effective
	// storage project as volume.
	for i, profile := range profiles {
		profileStorageProject := project.StorageVolumeProjectFromRecord(profileProjects[i], volumeType)

		// Check profile's storage project is the same as the volume's project.
		// If not then the volume names mentioned in the profile's config cannot be referring to volumes
		// in the volume's project we are trying to match, and this profile cannot possibly be using it.
		if projectName != profileStorageProject {
			continue
		}

		var usedByDevices []string

		// Iterate through each of the profiles's devices, looking for disks in the same pool as volume.
		// Then try and match the volume name against the profile device's "source" property.
		for name, dev := range profile.Devices {
			usesVol, err := volumeIsUsedByDevice(*vol, nil, dev)
			if err != nil {
				return err
			}

			if usesVol {
				usedByDevices = append(usedByDevices, name)
			}
		}

		if len(usedByDevices) > 0 {
			err = profileFunc(profileIDs[i], profile, *profileProjects[i], usedByDevices)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// VolumeUsedByInstanceDevices finds instances using a volume (either directly or via their expanded profiles if
// expandDevices is true) and passes them to instanceFunc for evaluation. If instanceFunc returns an error then it
// is returned immediately. The instanceFunc is executed during a DB transaction, so DB queries are not permitted.
// The instanceFunc is provided with a instance config, project config, instance's profiles and a list of device
// names that are using the volume.
func VolumeUsedByInstanceDevices(s *state.State, poolName string, projectName string, vol *api.StorageVolume, expandDevices bool, instanceFunc func(inst db.InstanceArgs, project api.Project, usedByDevices []string) error) error {
	// Convert the volume type name to our internal integer representation.
	volumeType, err := cluster.StoragePoolVolumeTypeFromName(vol.Type)
	if err != nil {
		return err
	}

	return s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			// If the volume has a specific cluster member which is different than the instance then skip as
			// instance cannot be using this volume.
			if vol.Location != "" && inst.Node != vol.Location {
				return nil
			}

			instStorageProject := project.StorageVolumeProjectFromRecord(&p, volumeType)

			// Check instance's storage project is the same as the volume's project.
			// If not then the volume names mentioned in the instance's config cannot be referring to volumes
			// in the volume's project we are trying to match, and this instance cannot possibly be using it.
			if projectName != instStorageProject {
				return nil
			}

			// Use local devices for usage check by if expandDevices is false (but don't modify instance).
			devices := inst.Devices

			// Expand devices for usage check if expandDevices is true.
			if expandDevices {
				devices = instancetype.ExpandInstanceDevices(devices.Clone(), inst.Profiles)
			}

			var usedByDevices []string

			// Iterate through each of the instance's devices, looking for disks in the same pool as volume.
			// Then try and match the volume name against the instance device's "source" property.
			for devName, dev := range devices {
				usesVol, err := volumeIsUsedByDevice(*vol, &inst, dev)
				if err != nil {
					return err
				}

				if usesVol {
					usedByDevices = append(usedByDevices, devName)
				}
			}

			if len(usedByDevices) > 0 {
				err = instanceFunc(inst, p, usedByDevices)
				if err != nil {
					return err
				}
			}

			return nil
		})
	})
}

// VolumeUsedByExclusiveRemoteInstancesWithProfiles checks if custom volume is exclusively attached to a remote
// instance. Returns the remote instance that has the volume exclusively attached. Returns nil if volume available.
func VolumeUsedByExclusiveRemoteInstancesWithProfiles(s *state.State, poolName string, projectName string, vol *api.StorageVolume) (*db.InstanceArgs, error) {
	pool, err := LoadByName(s, poolName)
	if err != nil {
		return nil, fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	info := pool.Driver().Info()

	// Always return nil if the storage driver supports mounting volumes on multiple nodes at once.
	if info.VolumeMultiNode {
		return nil, nil
	}

	// Find if volume is attached to a remote instance.
	var remoteInstance *db.InstanceArgs
	err = VolumeUsedByInstanceDevices(s, poolName, projectName, vol, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		if dbInst.Node != s.ServerName {
			remoteInstance = &dbInst
			return db.ErrListStop // Stop the search, this volume is attached to a remote instance.
		}

		return nil
	})
	if err != nil && err != db.ErrListStop {
		return nil, err
	}

	return remoteInstance, nil
}

// VolumeUsedByDaemon indicates whether the volume is used by daemon storage.
func VolumeUsedByDaemon(s *state.State, poolName string, volumeName string) (bool, error) {
	var storageBackups string
	var storageImages string
	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		storageBackups = nodeConfig.StorageBackupsVolume()
		storageImages = nodeConfig.StorageImagesVolume()

		return nil
	})
	if err != nil {
		return false, err
	}

	fullName := poolName + "/" + volumeName
	if storageBackups == fullName || storageImages == fullName {
		return true, nil
	}

	return false, nil
}

// FallbackMigrationType returns the fallback migration transport to use based on volume content type.
func FallbackMigrationType(contentType drivers.ContentType) migration.MigrationFSType {
	if drivers.IsContentBlock(contentType) {
		return migration.MigrationFSType_BLOCK_AND_RSYNC
	}

	return migration.MigrationFSType_RSYNC
}

// RenderSnapshotUsage can be used as an optional argument to Instance.Render() to return snapshot usage.
// As this is a relatively expensive operation it is provided as an optional feature rather than on by default.
func RenderSnapshotUsage(s *state.State, snapInst instance.Instance) func(response any) error {
	return func(response any) error {
		apiRes, ok := response.(*api.InstanceSnapshot)
		if !ok {
			return nil
		}

		pool, err := LoadByInstance(s, snapInst)
		if err == nil {
			// It is important that the snapshot not be mounted here as mounting a snapshot can trigger a very
			// expensive filesystem UUID regeneration, so we rely on the driver implementation to get the info
			// we are requesting as cheaply as possible.
			volumeState, err := pool.GetInstanceUsage(snapInst)
			if err == nil {
				apiRes.Size = volumeState.Used
			}
		}

		return nil
	}
}

// InstanceMount mounts an instance's storage volume (if not already mounted).
// Please call InstanceUnmount when finished.
func InstanceMount(pool Pool, inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	var err error
	var mountInfo *MountInfo

	if inst.IsSnapshot() {
		mountInfo, err = pool.MountInstanceSnapshot(inst, op)
		if err != nil {
			return nil, err
		}
	} else {
		mountInfo, err = pool.MountInstance(inst, op)
		if err != nil {
			return nil, err
		}
	}

	return mountInfo, nil
}

// InstanceUnmount unmounts an instance's storage volume (if not in use).
func InstanceUnmount(pool Pool, inst instance.Instance, op *operations.Operation) error {
	var err error

	if inst.IsSnapshot() {
		err = pool.UnmountInstanceSnapshot(inst, op)
	} else {
		err = pool.UnmountInstance(inst, op)
	}

	return err
}

// InstanceDiskBlockSize returns the block device size for the instance's disk.
// This will mount the instance if not already mounted and will unmount at the end if needed.
func InstanceDiskBlockSize(pool Pool, inst instance.Instance, op *operations.Operation) (int64, error) {
	mountInfo, err := InstanceMount(pool, inst, op)
	if err != nil {
		return -1, err
	}

	defer func() { _ = InstanceUnmount(pool, inst, op) }()

	devSource, isPath := mountInfo.DevSource.(deviceConfig.DevSourcePath)

	if !isPath {
		return -1, fmt.Errorf("Unhandled DevSource type %T", mountInfo.DevSource)
	}

	if devSource.Path == "" {
		return -1, fmt.Errorf("No disk path available from mount")
	}

	blockDiskSize, err := block.DiskSizeBytes(devSource.Path)
	if err != nil {
		return -1, fmt.Errorf("Error getting block disk size %q: %w", devSource.Path, err)
	}

	return blockDiskSize, nil
}

// ComparableSnapshot is used when comparing snapshots on different pools to see whether they differ.
type ComparableSnapshot struct {
	// Name of the snapshot (without the parent name).
	Name string

	// Identifier of the snapshot (that remains the same when copied between pools).
	ID string

	// Creation date time of the snapshot.
	CreationDate time.Time
}

// CompareSnapshots returns a list of snapshot indexes (from the associated input slices) to sync from the source
// and to delete from the target respectively.
// A snapshot will be added to "to sync from source" slice if it either doesn't exist in the target or its ID or
// creation date is different to the source.
// A snapshot will be added to the "to delete from target" slice if it doesn't exist in the source or its ID or
// creation date is different to the source.
func CompareSnapshots(sourceSnapshots []ComparableSnapshot, targetSnapshots []ComparableSnapshot) (syncSourceSnapshots []int, deleteTargetSnapshots []int) {
	// Compare source and target.
	sourceSnapshotsByName := make(map[string]*ComparableSnapshot, len(sourceSnapshots))
	targetSnapshotsByName := make(map[string]*ComparableSnapshot, len(targetSnapshots))

	var syncFromSource, deleteFromTarget []int

	// Generate a list of source snapshots by name.
	for sourceSnapIndex := range sourceSnapshots {
		sourceSnapshotsByName[sourceSnapshots[sourceSnapIndex].Name] = &sourceSnapshots[sourceSnapIndex]
	}

	// If target snapshot doesn't exist in source, or its creation date or ID differ,
	// then mark it for deletion on target.
	for targetSnapIndex := range targetSnapshots {
		// Generate a list of target snapshots by name for later comparison.
		targetSnapshotsByName[targetSnapshots[targetSnapIndex].Name] = &targetSnapshots[targetSnapIndex]

		sourceSnap, sourceSnapExists := sourceSnapshotsByName[targetSnapshots[targetSnapIndex].Name]
		if !sourceSnapExists || !sourceSnap.CreationDate.Equal(targetSnapshots[targetSnapIndex].CreationDate) || sourceSnap.ID != targetSnapshots[targetSnapIndex].ID {
			deleteFromTarget = append(deleteFromTarget, targetSnapIndex)
		}
	}

	// If source snapshot doesn't exist in target, or its creation date or ID differ,
	// then mark it for syncing to target.
	for sourceSnapIndex := range sourceSnapshots {
		targetSnap, targetSnapExists := targetSnapshotsByName[sourceSnapshots[sourceSnapIndex].Name]
		if !targetSnapExists || !targetSnap.CreationDate.Equal(sourceSnapshots[sourceSnapIndex].CreationDate) || targetSnap.ID != sourceSnapshots[sourceSnapIndex].ID {
			syncFromSource = append(syncFromSource, sourceSnapIndex)
		}
	}

	return syncFromSource, deleteFromTarget
}

// ValidVolumeName validates a volume name.
func ValidVolumeName(volumeName string) error {
	if volumeName == "" {
		return fmt.Errorf("Invalid volume name: Cannot be empty")
	}

	if strings.Contains(volumeName, "\\") {
		return fmt.Errorf("Invalid volume name %q: Cannot contain backslashes", volumeName)
	}

	if strings.Contains(volumeName, shared.SnapshotDelimiter) {
		return fmt.Errorf("Invalid volume name %q: Cannot contain slashes", volumeName)
	}

	return nil
}

// GetPoolDefaultBlockSize returns the default block size for the specified storage pool according to its driver.
func GetPoolDefaultBlockSize(s *state.State, poolName string) (string, error) {
	pool, err := LoadByName(s, poolName)
	if err != nil {
		return "", fmt.Errorf("Failed loading storage pool: %w", err)
	}

	return pool.Driver().Info().DefaultBlockSize, nil
}

// VolumeDetermineNextSnapshotName determines a name for next snapshot of a volume
// following the volume's snapshots.pattern or the provided default pattern.
func VolumeDetermineNextSnapshotName(ctx context.Context, s *state.State, pool string, volumeName string, volumeConfig map[string]string) (string, error) {
	var err error

	pattern, ok := volumeConfig["snapshots.pattern"]
	if !ok {
		pattern = defaultSnapshotPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		var i int
		_ = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			i = tx.GetNextStorageVolumeSnapshotIndex(ctx, pool, volumeName, cluster.StoragePoolVolumeTypeCustom, pattern)

			return nil
		})

		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	var snapshots []db.StorageVolumeArgs
	var projects []string
	var pools []string

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err = cluster.GetProjectNames(ctx, tx.Tx())
		if err != nil {
			return err
		}

		pools, err = tx.GetStoragePoolNames(ctx)
		if err != nil {
			return err
		}

		for _, pool := range pools {
			var poolID int64
			poolID, err = tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return err
			}

			for _, project := range projects {
				snaps, err := tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, project, volumeName, cluster.StoragePoolVolumeTypeCustom, poolID)
				if err != nil {
					return err
				}

				snapshots = append(snapshots, snaps...)
			}
		}

		for _, snap := range snapshots {
			_, snapOnlyName, _ := api.GetParentAndSnapshotName(snap.Name)

			if snapOnlyName == pattern {
				snapshotExists = true
				break
			}
		}

		if snapshotExists {
			i := tx.GetNextStorageVolumeSnapshotIndex(ctx, pool, volumeName, cluster.StoragePoolVolumeTypeCustom, pattern)
			pattern = strings.Replace(pattern, "%d", strconv.Itoa(i), 1)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return pattern, nil
}
