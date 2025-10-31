package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

type common struct {
	name        string
	config      map[string]string
	getVolID    func(volType VolumeType, volName string) (int64, error)
	commonRules *Validators
	state       *state.State
	logger      logger.Logger
	patches     map[string]func() error
}

func (d *common) init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators) {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.commonRules = commonRules
	d.state = state
	d.logger = logger
}

// isRemote returns false indicating this driver does not use remote storage.
func (d *common) isRemote() bool {
	return false
}

// defaultVMBlockFilesystemSize returns the default size for VM block filesystem
// devices.
func (d *common) defaultVMBlockFilesystemSize() string {
	return defaultVMBlockFilesystemSize
}

// defaultBlockVolumeSize returns the default size for block volumes in this pool.
func (d *common) defaultBlockVolumeSize() string {
	return defaultBlockSize
}

// validatePool validates a pool config against common rules and optional driver specific rules.
func (d *common) validatePool(config map[string]string, driverRules map[string]func(value string) error, volumeRules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.PoolRules()

	// Merge driver specific rules into common rules.
	maps.Copy(rules, driverRules)

	// Add to pool volume configuration options as volume.* options.
	// These will be used as default configuration options for volume.
	for volRule, volValidator := range volumeRules {
		rules["volume."+volRule] = volValidator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} // Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for option %q: %w", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		return fmt.Errorf("Invalid option %q", k)
	}

	return nil
}

// fillVolumeConfig populates volume config with defaults from pool.
// excludeKeys allow exclude some keys from copying to volume config.
// Sometimes that can be useful when copying is dependant from specific conditions
// and shouldn't be done in generic way.
func (d *common) fillVolumeConfig(vol *Volume, excludedKeys ...string) error {
	for k := range d.config {
		if !strings.HasPrefix(k, "volume.") {
			continue
		}

		volKey := strings.TrimPrefix(k, "volume.")

		isExcluded := slices.Contains(excludedKeys, volKey)

		if isExcluded {
			continue
		}

		// If volume type is not custom or bucket, don't copy "size" property to volume config.
		if (vol.volType != VolumeTypeCustom && vol.volType != VolumeTypeBucket) && volKey == "size" {
			continue
		}

		// security.shifted and security.unmapped are only relevant for custom filesystem volumes.
		if (vol.Type() != VolumeTypeCustom || vol.ContentType() != ContentTypeFS) && (volKey == "security.shifted" || volKey == "security.unmapped") {
			continue
		}

		// security.shared is only relevant for custom block volumes.
		if (vol.Type() != VolumeTypeCustom || vol.ContentType() != ContentTypeBlock) && (volKey == "security.shared") {
			continue
		}

		if vol.config[volKey] == "" {
			vol.config[volKey] = d.config[k]
		}
	}

	return nil
}

// FillVolumeConfig populate volume with default config.
func (d *common) FillVolumeConfig(vol Volume) error {
	return d.fillVolumeConfig(&vol)
}

// validateVolume validates a volume config against common rules and optional driver specific rules.
// This functions has a removeUnknownKeys option that if set to true will remove any unknown fields
// (excluding those starting with "user.") which can be used when translating a volume config to a
// different storage driver that has different options.
func (d *common) validateVolume(vol Volume, driverRules map[string]func(value string) error, removeUnknownKeys bool) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.VolumeRules(vol)

	// Merge driver specific rules into common rules.
	maps.Copy(rules, driverRules)

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} // Mark field as checked.
		err := validator(vol.config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for volume %q option %q: %w", vol.name, k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range vol.config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		if !removeUnknownKeys {
			return fmt.Errorf("Invalid option for volume %q option %q", vol.name, k)
		}

		delete(vol.config, k)
	}

	// If volume type is not custom or bucket, don't allow "size" property.
	if (vol.volType != VolumeTypeCustom && vol.volType != VolumeTypeBucket) && vol.config["size"] != "" {
		return fmt.Errorf("Volume %q property is not valid for volume type", "size")
	}

	// Check that security.unmapped and security.shifted are not set together.
	if shared.IsTrue(vol.config["security.unmapped"]) && shared.IsTrue(vol.config["security.shifted"]) {
		return errors.New("security.unmapped and security.shifted are mutually exclusive")
	}

	return nil
}

// SourceIdentifier returns a driver specific identifier for the configured source.
// How a storage pools actual source is configured depends on the driver.
// But the driver is capable of resolving the source using the provided config keys
// and returns an identifier which uniquely describes the underlying source.
func (d *common) SourceIdentifier() (string, error) {
	return "", ErrNotSupported
}

// ValidateSource validates the provided config keys can be used to specify the underlying source.
// The validation of the source is split into its own driver level function to allow being
// called when creating new pools but also when importing/reusing existing sources.
func (d *common) ValidateSource() error {
	return ErrNotSupported
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *common) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var transportType migration.MigrationFSType
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if IsContentBlock(contentType) {
		transportType = migration.MigrationFSType_BLOCK_AND_RSYNC
	} else {
		transportType = migration.MigrationFSType_RSYNC
	}

	return []migration.Type{
		{
			FSType:   transportType,
			Features: rsyncFeatures,
		},
	}
}

// Name returns the pool name.
func (d *common) Name() string {
	return d.name
}

// Logger returns the current logger.
func (d *common) Logger() logger.Logger {
	return d.logger
}

// Config returns the storage pool config (as a copy, so not modifiable).
func (d *common) Config() map[string]string {
	confCopy := make(map[string]string, len(d.config))
	maps.Copy(confCopy, d.config)

	return confCopy
}

// ApplyPatch looks for a suitable patch and runs it.
func (d *common) ApplyPatch(name string) error {
	if d.patches == nil {
		return fmt.Errorf("The patch mechanism isn't implemented on pool %q", d.name)
	}

	// Locate the patch.
	patch, ok := d.patches[name]
	if !ok {
		return fmt.Errorf("Patch %q isn't implemented on pool %q", name, d.name)
	}

	// Handle cases where a patch isn't needed.
	if patch == nil {
		return nil
	}

	return patch()
}

// moveGPTAltHeader moves the GPT alternative header to the end of the disk device supplied.
// If the device supplied is not detected as not being a GPT disk then no action is taken and nil is returned.
// If the required sgdisk command is not available a warning is logged, but no error is returned, as really it is
// the job of the VM quest to ensure the partitions are resized to the size of the disk (as LXD does not dicatate
// what partition structure (if any) the disk should have. However we do attempt to move the GPT alternative
// header where possible so that the backup header is where it is expected in case of any corruption with the
// primary header.
func (d *common) moveGPTAltHeader(devPath string) error {
	path, err := exec.LookPath("sgdisk")
	if err != nil {
		d.logger.Warn("Skipped moving GPT alternative header to end of disk as sgdisk command not found", logger.Ctx{"dev": devPath})
		return nil
	}

	// Our images and VM drives use a 512 bytes sector size.
	// If the underlying block device uses a different sector size, we need to fake the correct size through a
	// loop device so sgdisk can correctly re-locate the partition tables.
	if shared.IsBlockdevPath(devPath) {
		blockSize, err := block.DiskBlockSize(devPath)
		if err != nil {
			return fmt.Errorf("Failed getting block size for %q: %w", devPath, err)
		}

		if blockSize != 512 {
			devPath, err = block.LoopDeviceSetupAlign(devPath)
			if err != nil {
				return fmt.Errorf("Failed setting up loop device for %q: %w", devPath, err)
			}

			defer func() {
				err := loopDeviceAutoDetach(devPath)
				if err != nil {
					d.logger.Warn("Failed detaching loop device", logger.Ctx{"dev": devPath, "err": err})
				}
			}()
		}
	}

	_, err = shared.RunCommandContext(context.TODO(), path, "--move-second-header", devPath)
	if err == nil {
		d.logger.Debug("Moved GPT alternative header to end of disk", logger.Ctx{"dev": devPath})
		return nil
	}

	runErr, ok := err.(shared.RunError)
	if ok {
		exitError, ok := runErr.Unwrap().(*exec.ExitError)
		if ok {
			// sgdisk exit code 3 = “Non-GPT  disk  detected and no -g option, but operation requires a write action”
			// exit code 2 = “an error occurred while reading the partition table" (e.g. no GPT present or header CRC mismatch)
			// Treat both as non-error for raw/MBR images, since we only relocate a GPT alternative header if one exists.
			if exitError.ExitCode() == 2 || exitError.ExitCode() == 3 {
				return nil // Non-error as non-GPT disk specified.
			}
		}
	}

	return err
}

// runFiller runs the supplied filler, and setting the returned volume size back into filler.
func (d *common) runFiller(vol Volume, devPath string, filler *VolumeFiller, allowUnsafeResize bool) error {
	if filler == nil || filler.Fill == nil {
		return nil
	}

	vol.driver.Logger().Debug("Running filler function", logger.Ctx{"dev": devPath, "path": vol.MountPath()})
	volSize, err := filler.Fill(vol, devPath, allowUnsafeResize)
	if err != nil {
		return err
	}

	filler.Size = volSize

	return nil
}

// CreateVolume creates a new storage volume on disk.
func (d *common) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	return ErrNotSupported
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *common) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return nil, nil, ErrNotSupported
}

// CreateVolumeFromCopy copies an existing storage volume (with or without snapshots) into a new volume.
func (d *common) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	return ErrNotSupported
}

// CreateVolumeFromMigration creates a new volume (with or without snapshots) from a migration data stream.
func (d *common) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	return ErrNotSupported
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *common) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	return ErrNotSupported
}

// DeleteVolume destroys the on-disk state of a volume.
func (d *common) DeleteVolume(vol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *common) HasVolume(vol Volume) (bool, error) {
	return false, ErrNotSupported
}

// ValidateVolume validates the supplied volume config. Optionally removes invalid keys from the volume's config.
func (d *common) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return ErrNotSupported
}

// UpdateVolume applies the driver specific changes of a volume configuration change.
func (d *common) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	return ErrNotSupported
}

// GetVolumeUsage returns the disk space usage of a volume.
func (d *common) GetVolumeUsage(vol Volume) (int64, error) {
	return -1, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
func (d *common) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	return ErrNotSupported
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *common) GetVolumeDiskPath(vol Volume) (string, error) {
	return "", ErrNotSupported
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *common) ListVolumes() ([]Volume, error) {
	return nil, ErrNotSupported
}

// MountVolume sets up the volume for use.
func (d *common) MountVolume(vol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// UnmountVolume clears any runtime state for the volume.
// As driver doesn't have volumes to unmount it returns false indicating the volume was already unmounted.
func (d *common) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	return false, ErrNotSupported
}

// CanDelegateVolume checks whether the volume can be delegated.
func (d *common) CanDelegateVolume(vol Volume) bool {
	return false
}

// DelegateVolume delegates a volume.
func (d *common) DelegateVolume(vol Volume, pid int) error {
	return nil
}

// RenameVolume renames the volume and all related filesystem entries.
func (d *common) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	return ErrNotSupported
}

// MigrateVolume streams the volume (with or without snapshots).
func (d *common) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	return ErrNotSupported
}

// BackupVolume creates an exported version of a volume.
func (d *common) BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return ErrNotSupported
}

// CreateVolumeSnapshot creates a new snapshot.
func (d *common) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// DeleteVolumeSnapshot deletes a snapshot.
func (d *common) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// MountVolumeSnapshot makes the snapshot available for use.
func (d *common) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// UnmountVolumeSnapshot clears any runtime state for the snapshot.
func (d *common) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	return false, ErrNotSupported
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *common) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return nil, ErrNotSupported
}

// CheckVolumeSnapshots checks that the volume's snapshots, according to the storage driver, match those provided.
func (d *common) CheckVolumeSnapshots(vol Volume, snapVols []Volume, op *operations.Operation) error {
	// Use the volume's driver reference to pick the actual method as implemented by the driver.
	storageSnapshotNames, err := vol.driver.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	// Create a list of all wanted snapshots.
	wantedSnapshotNames := make([]string, 0, len(snapVols))
	for _, snap := range snapVols {
		_, snapName, _ := api.GetParentAndSnapshotName(snap.name)
		wantedSnapshotNames = append(wantedSnapshotNames, snapName)
	}

	// Check if the provided list of volume snapshots matches the ones from storage.
	for _, wantedSnapshotName := range wantedSnapshotNames {
		if !slices.Contains(storageSnapshotNames, wantedSnapshotName) {
			return fmt.Errorf("Snapshot %q expected but not in storage", wantedSnapshotName)
		}
	}

	// Check if the snapshots in storage match the ones from the provided list.
	for _, storageSnapshotName := range storageSnapshotNames {
		if !slices.Contains(wantedSnapshotNames, storageSnapshotName) {
			return fmt.Errorf("Snapshot %q in storage but not expected", storageSnapshotName)
		}
	}

	return nil
}

// RestoreVolume resets a volume to its snapshotted state.
func (d *common) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// RenameVolumeSnapshot renames a snapshot.
func (d *common) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	return ErrNotSupported
}

// ValidateBucket validates the supplied bucket name.
func (d *common) ValidateBucket(bucket Volume) error {
	projectName, bucketName := project.StorageVolumeParts(bucket.name)
	if projectName == "" {
		return errors.New("Project prefix missing in bucket volume name")
	}

	match, err := regexp.MatchString(`^[a-z0-9][\-\.a-z0-9]{2,62}$`, bucketName)
	if err != nil {
		return err
	}

	if !match {
		return errors.New("Bucket name must be between 3 and 63 lowercase letters, numbers, periods or hyphens and must start with a letter or number")
	}

	return nil
}

// GetBucketURL returns the URL of the specified bucket.
func (d *common) GetBucketURL(bucketName string) *url.URL {
	return nil
}

// CreateBucket creates a new bucket.
func (d *common) CreateBucket(bucket Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// DeleteBucket deletes an existing bucket.
func (d *common) DeleteBucket(bucket Volume, op *operations.Operation) error {
	return ErrNotSupported
}

// UpdateBucket updates an existing bucket.
func (d *common) UpdateBucket(bucket Volume, changedConfig map[string]string) error {
	return ErrNotSupported
}

// ValidateBucketKey validates the supplied bucket key config.
func (d *common) ValidateBucketKey(keyName string, creds S3Credentials, roleName string) error {
	if keyName == "" {
		return errors.New("Key name is required")
	}

	validRoles := []string{"admin", "read-only"}
	if !slices.Contains(validRoles, roleName) {
		return errors.New("Invalid key role")
	}

	return nil
}

// CreateBucketKey create bucket key.
func (d *common) CreateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error) {
	return nil, ErrNotSupported
}

// UpdateBucketKey updates bucket key.
func (d *common) UpdateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error) {
	return nil, ErrNotSupported
}

// DeleteBucketKey deletes the bucket key.
func (d *common) DeleteBucketKey(bucket Volume, keyName string, op *operations.Operation) error {
	return nil
}

// roundVolumeBlockSizeBytes returns sizeBytes rounded up to the next multiple
// of MinBlockBoundary.
func (d *common) roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) int64 {
	// QEMU requires image files to be in traditional storage block boundaries.
	// We use 8k here to ensure our images are compatible with all of our backend drivers.
	return roundAbove(MinBlockBoundary, sizeBytes)
}

func (d *common) isBlockBacked(vol Volume) bool {
	return vol.driver.Info().BlockBacking
}

// filesystemFreeze syncs and freezes a filesystem and returns an unfreeze function on success.
func (d *common) filesystemFreeze(path string) (func() error, error) {
	err := filesystem.SyncFS(path)
	if err != nil {
		return nil, fmt.Errorf("Failed syncing filesystem %q: %w", path, err)
	}

	_, err = shared.RunCommandContext(context.TODO(), "fsfreeze", "--freeze", path)
	if err != nil {
		return nil, fmt.Errorf("Failed freezing filesystem %q: %w", path, err)
	}

	d.logger.Info("Filesystem frozen", logger.Ctx{"path": path})

	unfreezeFS := func() error {
		_, err := shared.RunCommandContext(context.TODO(), "fsfreeze", "--unfreeze", path)
		if err != nil {
			return fmt.Errorf("Failed unfreezing filesystem %q: %w", path, err)
		}

		d.logger.Info("Filesystem unfrozen", logger.Ctx{"path": path})

		return nil
	}

	return unfreezeFS, nil
}
