package storage

import (
	"archive/tar"
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/backup"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/storage/memorypipe"
	"github.com/canonical/lxd/lxd/storage/s3"
	"github.com/canonical/lxd/lxd/storage/s3/miniod"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/version"
)

var unavailablePools = make(map[string]struct{})
var unavailablePoolsMu = sync.Mutex{}

// instanceDiskVolumeEffectiveFields fields from the instance disks that are applied to the volume's effective
// config (but not stored in the disk's volume database record).
var instanceDiskVolumeEffectiveFields = []string{
	"size",
	"size.state",
}

type lxdBackend struct {
	driver drivers.Driver
	id     int64
	db     api.StoragePool
	name   string
	state  *state.State
	logger logger.Logger
	nodes  map[int64]db.StoragePoolNode
}

// ID returns the storage pool ID.
func (b *lxdBackend) ID() int64 {
	return b.id
}

// Name returns the storage pool name.
func (b *lxdBackend) Name() string {
	return b.name
}

// Description returns the storage pool description.
func (b *lxdBackend) Description() string {
	return b.db.Description
}

// ValidateName validates the provided name, and returns an error if it's not a valid storage name.
func (b *lxdBackend) ValidateName(value string) error {
	if strings.Contains(value, "/") {
		return fmt.Errorf(`Storage name cannot contain "/"`)
	}

	for _, r := range value {
		if unicode.IsSpace(r) {
			return fmt.Errorf(`Storage name cannot contain white space`)
		}
	}

	return nil
}

// Validate storage pool config.
func (b *lxdBackend) Validate(config map[string]string) error {
	return b.Driver().Validate(config)
}

// Status returns the storage pool status.
func (b *lxdBackend) Status() string {
	return b.db.Status
}

// LocalStatus returns storage pool status of the local cluster member.
func (b *lxdBackend) LocalStatus() string {
	// Check if pool is unavailable locally and replace status if so.
	// But don't modify b.db.Status as the status may be recovered later so we don't want to persist it here.
	if !IsAvailable(b.name) {
		return api.StoragePoolStatusUnvailable
	}

	node, exists := b.nodes[b.state.DB.Cluster.GetNodeID()]
	if !exists {
		return api.StoragePoolStatusUnknown
	}

	return db.StoragePoolStateToAPIStatus(node.State)
}

// isStatusReady returns an error if pool is not ready for use on this server.
func (b *lxdBackend) isStatusReady() error {
	if b.Status() == api.StoragePoolStatusPending {
		return fmt.Errorf("Specified pool is not fully created")
	}

	if b.LocalStatus() == api.StoragePoolStatusUnvailable {
		return api.StatusErrorf(http.StatusServiceUnavailable, "Storage pool is unavailable on this server")
	}

	return nil
}

// ToAPI returns the storage pool as an API representation.
func (b *lxdBackend) ToAPI() api.StoragePool {
	return b.db
}

// Driver returns the storage pool driver.
func (b *lxdBackend) Driver() drivers.Driver {
	return b.driver
}

// MigrationTypes returns the migration transport method preferred when sending a migration,
// based on the migration method requested by the driver's ability. The snapshots argument
// indicates whether snapshots are migrated as well. It is used to determine whether to use
// optimized migration.
func (b *lxdBackend) MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return b.driver.MigrationTypes(contentType, refresh, copySnapshots)
}

// Create creates the storage pool layout on the storage device.
// localOnly is used for clustering where only a single node should do remote storage setup.
func (b *lxdBackend) Create(clientType request.ClientType, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"config": b.db.Config, "description": b.db.Description, "clientType": clientType})
	l.Debug("Create started")
	defer l.Debug("Create finished")

	// Validate name.
	err := b.ValidateName(b.name)
	if err != nil {
		return err
	}

	// Validate config.
	err = b.driver.Validate(b.db.Config)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	path := drivers.GetPoolMountPath(b.name)

	if shared.IsDir(path) {
		return fmt.Errorf("Storage pool directory %q already exists", path)
	}

	// Create the storage path.
	err = os.MkdirAll(path, 0711)
	if err != nil {
		return fmt.Errorf("Failed to create storage pool directory %q: %w", path, err)
	}

	revert.Add(func() { _ = os.RemoveAll(path) })

	if b.driver.Info().Remote && clientType != request.ClientTypeNormal {
		if !b.driver.Info().MountedRoot {
			// Create the directory structure.
			err = b.createStorageStructure(path)
			if err != nil {
				return err
			}
		}

		// Dealing with a remote storage pool, we're done now.
		revert.Success()
		return nil
	}

	// Create the storage pool on the storage device.
	err = b.driver.Create()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = b.driver.Delete(op) })

	// Mount the storage pool.
	ourMount, err := b.driver.Mount()
	if err != nil {
		return err
	}

	// We expect the caller of create to mount the pool if needed, so we should unmount after
	// storage struct has been created.
	if ourMount {
		defer func() { _, _ = b.driver.Unmount() }()
	}

	// Create the directory structure.
	err = b.createStorageStructure(path)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// GetNewVolume returns a drivers.Volume that doesn't yet exist in the database.
// It contains copies of the supplied volume config, including a new UUID, and the pools config.
// Use the returned drivers.Volume as the base for actions performed on the new volume.
func (b *lxdBackend) GetNewVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	newVol := b.GetVolume(volType, contentType, volName, volConfig)

	// Set a new UUID.
	newVol.Config()["volatile.uuid"] = uuid.New().String()
	return newVol
}

// GetVolume returns a drivers.Volume containing copies of the supplied volume config and the pools config.
func (b *lxdBackend) GetVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	return drivers.NewVolume(b.driver, b.name, volType, contentType, volName, volConfig, b.db.Config).Clone()
}

// GetResources returns utilisation information about the pool.
func (b *lxdBackend) GetResources() (*api.ResourcesStoragePool, error) {
	l := b.logger.AddContext(nil)
	l.Debug("GetResources started")
	defer l.Debug("GetResources finished")

	return b.driver.GetResources()
}

// IsUsed returns whether the storage pool is used by any volumes or profiles (excluding image volumes).
func (b *lxdBackend) IsUsed() (bool, error) {
	usedBy, err := UsedBy(context.TODO(), b.state, b, true, true, cluster.StoragePoolVolumeTypeNameImage)
	if err != nil {
		return false, err
	}

	return len(usedBy) > 0, nil
}

// Update updates the pool config.
func (b *lxdBackend) Update(clientType request.ClientType, newDesc string, newConfig map[string]string, _ *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"newDesc": newDesc, "newConfig": newConfig})
	l.Debug("Update started")
	defer l.Debug("Update finished")

	// Validate config.
	err := b.driver.Validate(newConfig)
	if err != nil {
		return err
	}

	// Diff the configurations.
	changedConfig, userOnly := b.detectChangedConfig(b.db.Config, newConfig)

	// Check if the pool source is being changed that the local state is still pending, otherwise prevent it.
	_, sourceChanged := changedConfig["source"]
	if sourceChanged && b.LocalStatus() != api.StoragePoolStatusPending {
		return fmt.Errorf("Pool source cannot be changed when not in pending state")
	}

	// Prevent shrinking the storage pool.
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		oldSizeBytes, _ := units.ParseByteSizeString(b.db.Config["size"])
		newSizeBytes, _ := units.ParseByteSizeString(newSize)

		if newSizeBytes < oldSizeBytes {
			return fmt.Errorf("Pool cannot be shrunk")
		}
	}

	// Apply changes to local member if both global pool and node are not pending and non-user config changed.
	// Otherwise just apply changes to DB (below) ready for the actual global create request to be initiated.
	if len(changedConfig) > 0 && b.Status() != api.StoragePoolStatusPending && b.LocalStatus() != api.StoragePoolStatusPending && !userOnly {
		err = b.driver.Update(changedConfig)
		if err != nil {
			return err
		}
	}

	// Update the database if something changed and we're in ClientTypeNormal mode.
	if clientType == request.ClientTypeNormal && (len(changedConfig) > 0 || newDesc != b.db.Description) {
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePool(ctx, b.name, newDesc, newConfig)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// warningsDelete deletes any persistent warnings for the pool.
func (b *lxdBackend) warningsDelete() error {
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteWarnings(ctx, tx.Tx(), cluster.EntityType(entity.TypeStoragePool), int(b.ID()))
	})
	if err != nil {
		return fmt.Errorf("Failed deleting persistent warnings: %w", err)
	}

	return nil
}

// Delete removes the pool.
func (b *lxdBackend) Delete(clientType request.ClientType, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"clientType": clientType})
	l.Debug("Delete started")
	defer l.Debug("Delete finished")

	// Delete any persistent warnings for pool.
	err := b.warningsDelete()
	if err != nil {
		return err
	}

	// If completely gone, just return
	path := shared.VarPath("storage-pools", b.name)
	if !shared.PathExists(path) {
		return nil
	}

	if clientType != request.ClientTypeNormal && b.driver.Info().Remote {
		if b.driver.Info().MountedRoot {
			_, err := b.driver.Unmount()
			if err != nil {
				return err
			}
		} else {
			// Remote storage may have leftover entries caused by
			// volumes that were moved or delete while a particular system was offline.
			err := os.RemoveAll(path)
			if err != nil {
				return err
			}
		}
	} else {
		// Remove any left over image volumes.
		// This can occur during partial image unpack or if the storage pool has been recovered from an
		// instace backup file and the image volume DB records were not restored.
		// If non-image volumes exist, we don't delete the, even if they can then prevent the storage pool
		// from being deleted, because they should not exist by this point and we don't want to end up
		// removing an instance or custom volume accidentally.
		// Errors listing volumes are ignored, as we should still try and delete the storage pool.
		vols, _ := b.driver.ListVolumes()
		for _, vol := range vols {
			if vol.Type() == drivers.VolumeTypeImage {
				err := b.driver.DeleteVolume(vol, op)
				if err != nil {
					return fmt.Errorf("Failed deleting left over image volume %q (%s): %w", vol.Name(), vol.ContentType(), err)
				}

				l.Warn("Deleted left over image volume", logger.Ctx{"volName": vol.Name(), "contentType": vol.ContentType()})
			}
		}

		// Delete the low-level storage.
		err := b.driver.Delete(op)
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint.
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove directory %q: %w", path, err)
	}

	unavailablePoolsMu.Lock()
	delete(unavailablePools, b.Name())
	unavailablePoolsMu.Unlock()

	return nil
}

// Mount mounts the storage pool.
func (b *lxdBackend) Mount() (bool, error) {
	b.logger.Debug("Mount started")
	defer b.logger.Debug("Mount finished")

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() {
		unavailablePoolsMu.Lock()
		unavailablePools[b.Name()] = struct{}{}
		unavailablePoolsMu.Unlock()
	})

	path := drivers.GetPoolMountPath(b.name)

	// Create the storage path if needed.
	if !shared.IsDir(path) {
		err := os.MkdirAll(path, 0711)
		if err != nil {
			return false, fmt.Errorf("Failed to create storage pool directory %q: %w", path, err)
		}
	}

	ourMount, err := b.driver.Mount()
	if err != nil {
		return false, err
	}

	if ourMount {
		revert.Add(func() { _, _ = b.Unmount() })
	}

	// Create the directory structure (if needed) after mounted.
	err = b.createStorageStructure(path)
	if err != nil {
		return false, err
	}

	revert.Success()

	// Ensure pool is marked as available now its mounted.
	unavailablePoolsMu.Lock()
	delete(unavailablePools, b.Name())
	unavailablePoolsMu.Unlock()

	return ourMount, nil
}

// Unmount unmounts the storage pool.
func (b *lxdBackend) Unmount() (bool, error) {
	b.logger.Debug("Unmount started")
	defer b.logger.Debug("Unmount finished")

	return b.driver.Unmount()
}

// ApplyPatch runs the requested patch at both backend and driver level.
func (b *lxdBackend) ApplyPatch(name string) error {
	b.logger.Info("Applying patch", logger.Ctx{"name": name})

	// Run early backend patches.
	patch, ok := lxdEarlyPatches[name]
	if ok {
		err := patch(b)
		if err != nil {
			return err
		}
	}

	// Run the driver patch itself.
	err := b.driver.ApplyPatch(name)
	if err != nil {
		return err
	}

	// Run late backend patches.
	patch, ok = lxdLatePatches[name]
	if ok {
		err := patch(b)
		if err != nil {
			return err
		}
	}

	return nil
}

// ensureInstanceSymlink creates a symlink in the instance directory to the instance's mount path
// if doesn't exist already.
func (b *lxdBackend) ensureInstanceSymlink(instanceType instancetype.Type, projectName string, instanceName string, mountPath string) error {
	if shared.IsSnapshot(instanceName) {
		return fmt.Errorf("Instance must not be snapshot")
	}

	symlinkPath := InstancePath(instanceType, projectName, instanceName, false)

	// Remove any old symlinks left over by previous bugs that may point to a different pool.
	if shared.PathExists(symlinkPath) {
		err := os.Remove(symlinkPath)
		if err != nil {
			return fmt.Errorf("Failed to remove symlink %q: %w", symlinkPath, err)
		}
	}

	// Create new symlink.
	err := os.Symlink(mountPath, symlinkPath)
	if err != nil {
		return fmt.Errorf("Failed to create symlink from %q to %q: %w", mountPath, symlinkPath, err)
	}

	return nil
}

// removeInstanceSymlink removes a symlink in the instance directory to the instance's mount path.
func (b *lxdBackend) removeInstanceSymlink(instanceType instancetype.Type, projectName string, instanceName string) error {
	symlinkPath := InstancePath(instanceType, projectName, instanceName, false)

	if shared.PathExists(symlinkPath) {
		err := os.Remove(symlinkPath)
		if err != nil {
			return fmt.Errorf("Failed to remove symlink %q: %w", symlinkPath, err)
		}
	}

	return nil
}

// ensureInstanceSnapshotSymlink creates a symlink in the snapshot directory to the instance's
// snapshot path if doesn't exist already.
func (b *lxdBackend) ensureInstanceSnapshotSymlink(instanceType instancetype.Type, projectName string, instanceName string) error {
	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(instanceType)
	if err != nil {
		return err
	}

	parentName, _, _ := api.GetParentAndSnapshotName(instanceName)
	snapshotSymlink := InstancePath(instanceType, projectName, parentName, true)
	volStorageName := project.Instance(projectName, parentName)

	snapshotTargetPath := drivers.GetVolumeSnapshotDir(b.name, volType, volStorageName)

	// Remove any old symlinks left over by previous bugs that may point to a different pool.
	if shared.PathExists(snapshotSymlink) {
		err = os.Remove(snapshotSymlink)
		if err != nil {
			return fmt.Errorf("Failed to remove symlink %q: %w", snapshotSymlink, err)
		}
	}

	// Create new symlink.
	err = os.Symlink(snapshotTargetPath, snapshotSymlink)
	if err != nil {
		return fmt.Errorf("Failed to create symlink from %q to %q: %w", snapshotTargetPath, snapshotSymlink, err)
	}

	return nil
}

// removeInstanceSnapshotSymlinkIfUnused removes the symlink in the snapshot directory to the
// instance's snapshot path if the snapshot path is missing. It is expected that the driver will
// remove the instance's snapshot path after the last snapshot is removed or the volume is deleted.
func (b *lxdBackend) removeInstanceSnapshotSymlinkIfUnused(instanceType instancetype.Type, projectName string, instanceName string) error {
	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(instanceType)
	if err != nil {
		return err
	}

	parentName, _, _ := api.GetParentAndSnapshotName(instanceName)
	snapshotSymlink := InstancePath(instanceType, projectName, parentName, true)
	volStorageName := project.Instance(projectName, parentName)

	snapshotTargetPath := drivers.GetVolumeSnapshotDir(b.name, volType, volStorageName)

	// If snapshot parent directory doesn't exist, remove symlink.
	if !shared.PathExists(snapshotTargetPath) {
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return fmt.Errorf("Failed to remove symlink %q: %w", snapshotSymlink, err)
			}
		}
	}

	return nil
}

// applyInstanceRootDiskOverrides applies the instance's root disk config to the volume's config.
func (b *lxdBackend) applyInstanceRootDiskOverrides(inst instance.Instance, vol *drivers.Volume) error {
	_, rootDiskConf, err := instancetype.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	for _, k := range instanceDiskVolumeEffectiveFields {
		if rootDiskConf[k] != "" {
			switch k {
			case "size":
				vol.SetConfigSize(rootDiskConf[k])
			case "size.state":
				vol.SetConfigStateSize(rootDiskConf[k])
			default:
				return fmt.Errorf("Unsupported instance disk volume override field %q", k)
			}
		}
	}

	return nil
}

// applyInstanceRootDiskInitialValues applies the instance's root disk initial config to the volume's config.
func (b *lxdBackend) applyInstanceRootDiskInitialValues(inst instance.Instance, volConfig map[string]string) error {
	_, rootDiskConf, err := instancetype.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	for k, v := range rootDiskConf {
		prefix, newKey, found := strings.Cut(k, "initial.")
		if found && prefix == "" {
			volConfig[newKey] = v
		}
	}

	return nil
}

// CreateInstance creates an empty instance.
func (b *lxdBackend) CreateInstance(inst instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("CreateInstance started")
	defer l.Debug("CreateInstance finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	revert := revert.New()
	defer revert.Fail()

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetNewVolume(volType, contentType, volStorageName, map[string]string{})

	err = b.applyInstanceRootDiskInitialValues(inst, vol.Config())
	if err != nil {
		return err
	}

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), "", volType, false, vol.Config(), inst.CreationDate(), time.Time{}, contentType, true, false)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	err = b.driver.CreateVolume(vol, nil, op)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = b.DeleteInstance(inst, op) })

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	err = inst.DeferTemplateApply(instance.TemplateTriggerCreate)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// CreateInstanceFromBackup restores a backup file onto the storage device. Because the backup file
// is unpacked and restored onto the storage device before the instance is created in the database
// it is necessary to return two functions; a post hook that can be run once the instance has been
// created in the database to run any storage layer finalisations, and a revert hook that can be
// run if the instance database load process fails that will remove anything created thus far.
func (b *lxdBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error) {
	l := b.logger.AddContext(logger.Ctx{"project": srcBackup.Project, "instance": srcBackup.Name, "snapshots": srcBackup.Snapshots, "optimizedStorage": *srcBackup.OptimizedStorage})
	l.Debug("CreateInstanceFromBackup started")
	defer l.Debug("CreateInstanceFromBackup finished")

	// Validate the names in the backup.yaml file as these could be malicious.
	err := instancetype.ValidName(srcBackup.Name, false)
	if err != nil {
		return nil, nil, err
	}

	err = instancetype.ValidName(srcBackup.Config.Container.Name, false)
	if err != nil {
		return nil, nil, err
	}

	for _, snapName := range srcBackup.Snapshots {
		snapInstName := srcBackup.Name + shared.SnapshotDelimiter + snapName
		err = instancetype.ValidName(snapInstName, true)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snap := range srcBackup.Config.Snapshots {
		snapInstName := srcBackup.Name + shared.SnapshotDelimiter + snap.Name
		err = instancetype.ValidName(snapInstName, true)
		if err != nil {
			return nil, nil, err
		}
	}

	// Get the volume name on storage.
	volStorageName := project.Instance(srcBackup.Project, srcBackup.Name)

	// Get the instance type.
	instanceType, err := instancetype.New(string(srcBackup.Type))
	if err != nil {
		return nil, nil, err
	}

	// Get the volume type.
	volType, err := InstanceTypeToVolumeType(instanceType)
	if err != nil {
		return nil, nil, err
	}

	contentType := drivers.ContentTypeFS
	if volType == drivers.VolumeTypeVM {
		contentType = drivers.ContentTypeBlock
	}

	var volumeConfig map[string]string

	if srcBackup.Config != nil && srcBackup.Config.Volume != nil {
		err = ValidVolumeName(srcBackup.Config.Volume.Name)
		if err != nil {
			return nil, nil, err
		}

		volumeConfig = srcBackup.Config.Volume.Config
	}

	// Don't use GetNewVolume as the new volume' UUID got already set beforehand.
	vol := b.GetVolume(volType, contentType, volStorageName, volumeConfig)

	sourceSnapshots := make([]drivers.Volume, 0, len(srcBackup.Config.VolumeSnapshots))
	for _, volSnap := range srcBackup.Config.VolumeSnapshots {
		err = ValidVolumeName(volSnap.Name)
		if err != nil {
			return nil, nil, err
		}

		snapshotName := drivers.GetSnapshotVolumeName(srcBackup.Name, volSnap.Name)
		snapshotStorageName := project.Instance(srcBackup.Project, snapshotName)

		// Don't use GetNewVolume as the new snapshot's UUID got already set beforehand.
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, volSnap.Config))
	}

	importRevert := revert.New()
	defer importRevert.Fail()

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	// Unpack the backup into the new storage volume(s).
	volPostHook, revertHook, err := b.driver.CreateVolumeFromBackup(volCopy, srcBackup, srcData, op)
	if err != nil {
		return nil, nil, err
	}

	if revertHook != nil {
		importRevert.Add(revertHook)
	}

	err = b.ensureInstanceSymlink(instanceType, srcBackup.Project, srcBackup.Name, vol.MountPath())
	if err != nil {
		return nil, nil, err
	}

	importRevert.Add(func() {
		_ = b.removeInstanceSymlink(instanceType, srcBackup.Project, srcBackup.Name)
	})

	if len(srcBackup.Snapshots) > 0 {
		err = b.ensureInstanceSnapshotSymlink(instanceType, srcBackup.Project, srcBackup.Name)
		if err != nil {
			return nil, nil, err
		}

		importRevert.Add(func() {
			_ = b.removeInstanceSnapshotSymlinkIfUnused(instanceType, srcBackup.Project, srcBackup.Name)
		})
	}

	// Update information in the backup.yaml file.
	err = vol.MountTask(func(mountPath string, _ *operations.Operation) error {
		return backup.UpdateInstanceConfig(b.state.DB.Cluster, srcBackup, mountPath)
	}, op)
	if err != nil {
		return nil, nil, fmt.Errorf("Error updating backup file: %w", err)
	}

	// Create a post hook function that will use the instance (that will be created) to setup a new volume
	// containing the instance's root disk device's config so that the driver's post hook function can access
	// that config to perform any post instance creation setup.
	postHook := func(inst instance.Instance) error {
		l.Debug("CreateInstanceFromBackup post hook started")
		defer l.Debug("CreateInstanceFromBackup post hook finished")

		postHookRevert := revert.New()
		defer postHookRevert.Fail()

		// Create database entry for new storage volume.
		var volumeDescription string
		var volumeConfig map[string]string
		volumeCreationDate := inst.CreationDate()

		if srcBackup.Config != nil && srcBackup.Config.Volume != nil {
			// If the backup restore interface provides volume config use it, otherwise use
			// default volume config for the storage pool.
			volumeDescription = srcBackup.Config.Volume.Description
			volumeConfig = srcBackup.Config.Volume.Config

			// Use volume's creation date if available.
			if !srcBackup.Config.Volume.CreatedAt.IsZero() {
				volumeCreationDate = srcBackup.Config.Volume.CreatedAt
			}
		}

		// Validate config and create database entry for new storage volume.
		// Strip unsupported config keys (in case the export was made from a different type of storage pool).
		err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), volumeDescription, volType, false, volumeConfig, volumeCreationDate, time.Time{}, contentType, true, true)
		if err != nil {
			return err
		}

		postHookRevert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

		for i, backupFileSnap := range srcBackup.Snapshots {
			var volumeSnapDescription string
			var volumeSnapConfig map[string]string
			var volumeSnapExpiryDate time.Time
			var volumeSnapCreationDate time.Time

			// Check if snapshot volume config is available for restore and matches snapshot name.
			if srcBackup.Config != nil {
				if len(srcBackup.Config.Snapshots) >= i-1 && srcBackup.Config.Snapshots[i] != nil && srcBackup.Config.Snapshots[i].Name == backupFileSnap {
					// Use instance snapshot's creation date if snap info available.
					volumeSnapCreationDate = srcBackup.Config.Snapshots[i].CreatedAt
				}

				if len(srcBackup.Config.VolumeSnapshots) >= i-1 && srcBackup.Config.VolumeSnapshots[i] != nil && srcBackup.Config.VolumeSnapshots[i].Name == backupFileSnap {
					// If the backup restore interface provides volume snapshot config use it,
					// otherwise use default volume config for the storage pool.
					volumeSnapDescription = srcBackup.Config.VolumeSnapshots[i].Description
					volumeSnapConfig = srcBackup.Config.VolumeSnapshots[i].Config

					if srcBackup.Config.VolumeSnapshots[i].ExpiresAt != nil {
						volumeSnapExpiryDate = *srcBackup.Config.VolumeSnapshots[i].ExpiresAt
					}

					// Use volume's creation date if available.
					if !srcBackup.Config.VolumeSnapshots[i].CreatedAt.IsZero() {
						volumeSnapCreationDate = srcBackup.Config.VolumeSnapshots[i].CreatedAt
					}
				}
			}

			newSnapshotName := drivers.GetSnapshotVolumeName(inst.Name(), backupFileSnap)

			// Validate config and create database entry for new storage volume.
			// Strip unsupported config keys (in case the export was made from a different type of storage pool).
			err = VolumeDBCreate(b, inst.Project().Name, newSnapshotName, volumeSnapDescription, volType, true, volumeSnapConfig, volumeSnapCreationDate, volumeSnapExpiryDate, contentType, true, true)
			if err != nil {
				return err
			}

			postHookRevert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, newSnapshotName, volType) })
		}

		// Generate the effective root device volume for instance.
		volStorageName := project.Instance(inst.Project().Name, inst.Name())
		vol := b.GetVolume(volType, contentType, volStorageName, volumeConfig)
		err = b.applyInstanceRootDiskOverrides(inst, &vol)
		if err != nil {
			return err
		}

		// Save any changes that have occurred to the instance's config to the on-disk backup.yaml file.
		err = b.UpdateInstanceBackupFile(inst, false, op)
		if err != nil {
			return fmt.Errorf("Failed updating backup file: %w", err)
		}

		// If the driver returned a post hook, run it now.
		if volPostHook != nil {
			// Initialise new volume containing root disk config supplied in instance.
			err = volPostHook(vol)
			if err != nil {
				return err
			}
		}

		rootDiskConf := vol.Config()

		// Apply quota config from root device if its set. Should be done after driver's post hook if set
		// so that any volume initialisation has been completed first.
		if rootDiskConf["size"] != "" {
			size := rootDiskConf["size"]
			l.Debug("Applying volume quota from root disk config", logger.Ctx{"size": size})

			allowUnsafeResize := false

			if vol.Type() == drivers.VolumeTypeContainer {
				// Enable allowUnsafeResize for container imports so that filesystem resize
				// safety checks are avoided in order to allow more imports to succeed when
				// otherwise the pre-resize estimated checks of resize2fs would prevent
				// import. If there is truly insufficient size to complete the import the
				// resize will still fail, but its OK as we will then delete the volume
				// rather than leaving it in a corrupted state. We don't need to do this
				// for non-container volumes (nor should we) because block volumes won't
				// error if we shrink them too much, and custom volumes can be created at
				// the correct size immediately and don't need a post-import resize step.
				allowUnsafeResize = true
			}

			err = b.driver.SetVolumeQuota(vol, size, allowUnsafeResize, op)
			if err != nil {
				// The restored volume can end up being larger than the root disk config's size
				// property due to the block boundary rounding some storage drivers use. As such
				// if the restored volume is larger than the config's size and it cannot be shrunk
				// to the equivalent size on the target storage driver, don't fail as the backup
				// has still been restored successfully.
				if !errors.Is(err, drivers.ErrCannotBeShrunk) {
					return fmt.Errorf("Failed applying volume quota to root disk: %w", err)
				}

				l.Warn("Could not apply volume quota from root disk config as restored volume cannot be shrunk", logger.Ctx{"size": size})
			}

			// Apply the filesystem volume quota (only when main volume is block).
			if vol.IsVMBlock() {
				vmStateSize := rootDiskConf["size.state"]

				// Apply default VM config filesystem size if main volume size is specified and
				// no custom vmStateSize is specified. This way if the main volume size is empty
				// (i.e removing quota) then this will also pass empty quota for the config
				// filesystem volume as well, allowing a former quota to be removed from both
				// volumes.
				if vmStateSize == "" && size != "" {
					vmStateSize = b.driver.Info().DefaultVMBlockFilesystemSize
				}

				l.Debug("Applying filesystem volume quota from root disk config", logger.Ctx{"size.state": vmStateSize})

				fsVol := vol.NewVMBlockFilesystemVolume()
				err := b.driver.SetVolumeQuota(fsVol, vmStateSize, allowUnsafeResize, op)
				if errors.Is(err, drivers.ErrCannotBeShrunk) {
					l.Warn("Could not apply VM filesystem volume quota from root disk config as restored volume cannot be shrunk", logger.Ctx{"size": vmStateSize})
				} else if err != nil {
					return fmt.Errorf("Failed applying filesystem volume quota to root disk: %w", err)
				}
			}
		}

		postHookRevert.Success()
		return nil
	}

	importRevert.Success()
	return postHook, revertHook, nil
}

// CreateInstanceFromCopy copies an instance volume and optionally its snapshots to new volume(s).
func (b *lxdBackend) CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "src": src.Name(), "snapshots": snapshots})
	l.Debug("CreateInstanceFromCopy started")
	defer l.Debug("CreateInstanceFromCopy finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if inst.Type() != src.Type() {
		return fmt.Errorf("Instance types must match")
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Get the source storage pool.
	srcPool, err := LoadByInstance(b.state, src)
	if err != nil {
		return err
	}

	srcPoolBackend, ok := srcPool.(*lxdBackend)
	if !ok {
		return fmt.Errorf("Source pool is not a lxdBackend")
	}

	// Check source volume exists, and get its config including all of the snapshots.
	srcConfig, err := srcPool.GenerateInstanceBackupConfig(src, true, op)
	if err != nil {
		return fmt.Errorf("Failed generating instance copy config: %w", err)
	}

	// Use the information from the backup config to create a list of all the source volume's snapshots.
	// This way we don't have to retrieve them separately from the database.
	sourceSnapshots := make([]drivers.Volume, 0, len(srcConfig.VolumeSnapshots))
	for _, sourceSnap := range srcConfig.VolumeSnapshots {
		snapshotName := drivers.GetSnapshotVolumeName(src.Name(), sourceSnap.Name)
		snapshotStorageName := project.Instance(src.Project().Name, snapshotName)
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, sourceSnap.Config))
	}

	// Unset the snapshots in the backup config if not requested by the caller.
	// Those were only required to create the list of source volume snapshots.
	if !snapshots {
		srcConfig.Snapshots = nil
		srcConfig.VolumeSnapshots = nil
	}

	// If we are copying snapshots, retrieve a list of snapshots from source volume.
	var snapshotNames []string
	if snapshots {
		snapshotNames = make([]string, 0, len(srcConfig.VolumeSnapshots))
		for _, snapshot := range srcConfig.VolumeSnapshots {
			snapshotNames = append(snapshotNames, snapshot.Name)
		}
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetNewVolume(volType, contentType, volStorageName, srcConfig.Volume.Config)

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		return fmt.Errorf("Cannot create volume, already exists on target storage")
	}

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Some driver backing stores require that running instances be frozen during copy.
	if !src.IsSnapshot() && srcPoolBackend.driver.Info().RunningCopyFreeze && src.IsRunning() && !src.IsFrozen() && !allowInconsistent {
		b.logger.Info("Freezing instance for consistent copy")
		err = src.Freeze()
		if err != nil {
			return err
		}

		defer func() { _ = src.Unfreeze() }()

		// Attempt to sync the filesystem.
		_ = filesystem.SyncFS(src.RootfsPath())
	}

	revert.Add(func() { _ = b.DeleteInstance(inst, op) })

	if b.Name() == srcPool.Name() {
		l.Debug("CreateInstanceFromCopy same-pool mode detected")

		// Validate config and create database entry for new storage volume.
		err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), "", vol.Type(), false, vol.Config(), inst.CreationDate(), time.Time{}, contentType, false, true)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

		targetSnapshots := make([]drivers.Volume, 0, len(snapshotNames))

		// Create database entries for new storage volume snapshots.
		for i, snapName := range snapshotNames {
			newSnapshotName := drivers.GetSnapshotVolumeName(inst.Name(), snapName)
			var volumeSnapExpiryDate time.Time
			if srcConfig.VolumeSnapshots[i].ExpiresAt != nil {
				volumeSnapExpiryDate = *srcConfig.VolumeSnapshots[i].ExpiresAt
			}

			newSnapshotStorageName := project.Instance(inst.Project().Name, newSnapshotName)
			snapVol := b.GetNewVolume(volType, contentType, newSnapshotStorageName, srcConfig.VolumeSnapshots[i].Config)

			// Validate config and create database entry for new storage volume.
			err = VolumeDBCreate(b, inst.Project().Name, newSnapshotName, srcConfig.VolumeSnapshots[i].Description, vol.Type(), true, snapVol.Config(), srcConfig.VolumeSnapshots[i].CreatedAt, volumeSnapExpiryDate, vol.ContentType(), false, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, newSnapshotName, vol.Type()) })

			targetSnapshots = append(targetSnapshots, snapVol)
		}

		// Generate the effective root device volume for instance.
		err = b.applyInstanceRootDiskOverrides(inst, &vol)
		if err != nil {
			return err
		}

		// Get the src volume name on storage.
		srcVolStorageName := project.Instance(src.Project().Name, src.Name())
		srcVol := b.GetVolume(volType, contentType, srcVolStorageName, srcConfig.Volume.Config)

		// Set the parent volume's UUID.
		if b.driver.Info().PopulateParentVolumeUUID {
			parentUUID, err := b.getParentVolumeUUID(srcVol, src.Project().Name)
			if err != nil {
				return err
			}

			srcVol.SetParentUUID(parentUUID)
		}

		volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)
		srcVolCopy := drivers.NewVolumeCopy(srcVol, sourceSnapshots...)

		err = b.driver.CreateVolumeFromCopy(volCopy, srcVolCopy, allowInconsistent, op)
		if err != nil {
			return err
		}
	} else {
		// We are copying volumes between storage pools so use migration system as it will
		// be able to negotiate a common transfer method between pool types.
		l.Debug("CreateInstanceFromCopy cross-pool mode detected")

		// Negotiate the migration type to use.
		offeredTypes := srcPool.MigrationTypes(contentType, false, snapshots)
		offerHeader := migration.TypesToHeader(offeredTypes...)
		migrationTypes, err := migration.MatchTypes(offerHeader, FallbackMigrationType(contentType), b.MigrationTypes(contentType, false, snapshots))
		if err != nil {
			return fmt.Errorf("Failed to negotiate copy migration type: %w", err)
		}

		var srcVolumeSize int64

		// For VMs, get source volume size so that target can create the volume the same size.
		if src.Type() == instancetype.VM {
			srcVolumeSize, err = InstanceDiskBlockSize(srcPool, src, op)
			if err != nil {
				return fmt.Errorf("Failed getting source disk size: %w", err)
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Run sender and receiver in separate go routines to prevent deadlocks.
		g, ctx := errgroup.WithContext(ctx)

		// Use in-memory pipe pair to simulate a connection between the sender and receiver.
		// Use context from error group so that if either side fails the pipes are closed.
		aEnd, bEnd := memorypipe.NewPipePair(ctx)

		// Start each side of the migration concurrently and collect any errors.
		g.Go(func() error {
			return srcPool.MigrateInstance(src, aEnd, &migration.VolumeSourceArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               src.Name(),
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				TrackProgress:      true, // Do use a progress tracker on sender.
				AllowInconsistent:  allowInconsistent,
				VolumeOnly:         !snapshots,
				Info:               &migration.Info{Config: srcConfig},
			}, op)
		})

		g.Go(func() error {
			return b.CreateInstanceFromMigration(inst, bEnd, migration.VolumeTargetArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               inst.Name(),
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				VolumeSize:         srcVolumeSize, // Block size setting override.
				TrackProgress:      false,         // Do not use a progress tracker on receiver.
				VolumeOnly:         !snapshots,
			}, op)
		})

		err = g.Wait()
		if err != nil {
			return fmt.Errorf("Create instance volume from copy failed: %w", err)
		}
	}

	// Setup the symlinks.
	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	if len(snapshotNames) > 0 {
		err = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, inst.Name())
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// RefreshCustomVolume refreshes custom volumes (and optionally snapshots) during the custom volume copy operations.
// Snapshots that are not present in the source but are in the destination are removed from the
// destination if snapshots are included in the synchronization.
func (b *lxdBackend) RefreshCustomVolume(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "srcProjectName": srcProjectName, "volName": volName, "desc": desc, "config": config, "srcPoolName": srcPoolName, "srcVolName": srcVolName, "snapshots": snapshots})
	l.Debug("RefreshCustomVolume started")
	defer l.Debug("RefreshCustomVolume finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if srcProjectName == "" {
		srcProjectName = projectName
	}

	// Setup the source pool backend instance.
	var srcPool Pool
	if b.name == srcPoolName {
		srcPool = b // Source and target are in the same pool so share pool var.
	} else {
		// Source is in a different pool to target, so load the pool.
		srcPool, err = LoadByName(b.state, srcPoolName)
		if err != nil {
			return err
		}
	}

	// Check source volume exists and is custom type, and get its config including all of the snapshots.
	srcConfig, err := srcPool.GenerateCustomVolumeBackupConfig(srcProjectName, srcVolName, true, op)
	if err != nil {
		return fmt.Errorf("Failed generating volume refresh config: %w", err)
	}

	// Use the source volume's description if not supplied.
	if desc == "" {
		desc = srcConfig.Volume.Description
	}

	contentDBType, err := cluster.StoragePoolVolumeContentTypeFromName(srcConfig.Volume.ContentType)
	if err != nil {
		return err
	}

	// Get the source volume's content type.
	contentType := VolumeDBContentTypeToContentType(contentDBType)

	if contentType != drivers.ContentTypeFS && contentType != drivers.ContentTypeBlock {
		return fmt.Errorf("Volume of content type %q cannot be refreshed", contentType)
	}

	storagePoolSupported := false
	for _, supportedType := range b.Driver().Info().VolumeTypes {
		if supportedType == drivers.VolumeTypeCustom {
			storagePoolSupported = true
			break
		}
	}

	if !storagePoolSupported {
		return fmt.Errorf("Storage pool does not support custom volume type")
	}

	// Use the information from the backup config to create a list of all the source volume's snapshots.
	// This way we don't have to retrieve them separately from the database.
	sourceSnapshots := make([]drivers.Volume, 0, len(srcConfig.VolumeSnapshots))
	for _, sourceSnap := range srcConfig.VolumeSnapshots {
		snapshotName := drivers.GetSnapshotVolumeName(srcConfig.Volume.Name, sourceSnap.Name)
		snapshotStorageName := project.StorageVolume(srcProjectName, snapshotName)
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, sourceSnap.Config))
	}

	// Unset the snapshots in the backup config if not requested by the caller.
	// Those were only required to create the list of source volume snapshots.
	if !snapshots {
		srcConfig.VolumeSnapshots = nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Load the target volume from database.
	dbVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeType(srcConfig.Volume.Type))
	if err != nil {
		return err
	}

	// Only send the snapshots that the target needs when refreshing.
	// There is currently no recorded creation timestamp, so we can only detect changes based on name.
	var snapshotNames []string
	if snapshots {
		// Compare snapshots.
		sourceSnapshotComparable := make([]ComparableSnapshot, 0, len(srcConfig.VolumeSnapshots))
		for _, sourceSnap := range srcConfig.VolumeSnapshots {
			sourceSnapshotComparable = append(sourceSnapshotComparable, ComparableSnapshot{
				Name:         sourceSnap.Name,
				CreationDate: sourceSnap.CreatedAt,
			})
		}

		// Get a list of already existing snapshots on the target volume.
		targetSnaps, err := VolumeDBSnapshotsGet(b, projectName, dbVol.Name, drivers.VolumeTypeCustom)
		if err != nil {
			return err
		}

		targetSnapshotsComparable := make([]ComparableSnapshot, 0, len(targetSnaps))
		for _, targetSnap := range targetSnaps {
			_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name)

			targetSnapshotsComparable = append(targetSnapshotsComparable, ComparableSnapshot{
				Name:         targetSnapName,
				CreationDate: targetSnap.CreationDate,
			})
		}

		syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

		// Delete extra snapshots first.
		for _, deleteTargetSnapIndex := range deleteTargetSnapshotIndexes {
			err = b.DeleteCustomVolumeSnapshot(projectName, targetSnaps[deleteTargetSnapIndex].Name, op)
			if err != nil {
				return err
			}
		}

		// Ensure that only the requested snapshots are included in the source config.
		allSnapshots := srcConfig.VolumeSnapshots
		srcConfig.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(syncSourceSnapshotIndexes))
		for _, syncSourceSnapIndex := range syncSourceSnapshotIndexes {
			snapshotNames = append(snapshotNames, allSnapshots[syncSourceSnapIndex].Name)
			srcConfig.VolumeSnapshots = append(srcConfig.VolumeSnapshots, allSnapshots[syncSourceSnapIndex])
		}
	}

	volStorageName := project.StorageVolume(projectName, dbVol.Name)
	vol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, dbVol.Config)

	// Get the src volume name on storage.
	srcVolStorageName := project.StorageVolume(srcProjectName, srcConfig.Volume.Name)
	srcVol := srcPool.GetVolume(drivers.VolumeTypeCustom, contentType, srcVolStorageName, srcConfig.Volume.Config)

	if srcPool == b {
		l.Debug("RefreshCustomVolume same-pool mode detected")

		// Only refresh the snapshots that the target needs.
		srcSnapVols := make([]string, 0, len(srcConfig.VolumeSnapshots))
		for _, srcSnap := range srcConfig.VolumeSnapshots {
			newSnapshotName := drivers.GetSnapshotVolumeName(dbVol.Name, srcSnap.Name)
			snapExpiryDate := time.Time{}
			if srcSnap.ExpiresAt != nil {
				snapExpiryDate = *srcSnap.ExpiresAt
			}

			// Generate source snapshot volumes list.
			targetSnapVolStorageName := project.StorageVolume(projectName, newSnapshotName)
			targetSnapVol := b.GetNewVolume(drivers.VolumeTypeCustom, contentType, targetSnapVolStorageName, srcSnap.Config)

			// Validate config and create database entry for new storage volume from source volume config.
			err = VolumeDBCreate(b, projectName, newSnapshotName, srcSnap.Description, drivers.VolumeTypeCustom, true, targetSnapVol.Config(), srcSnap.CreatedAt, snapExpiryDate, contentType, false, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, projectName, newSnapshotName, vol.Type()) })

			// Extend the list of snapshots that require refresh.
			srcSnapVols = append(srcSnapVols, srcSnap.Name)
		}

		// Reload the final target volume's snapshots.
		// Some snapshots might have been removed from the target volume if they aren't anymore present on the source volume.
		// Other snapshots might require a refresh if they have been deleted from the target volume in the meantime.
		// Get the snapshots directly from the database to ensure they are in the right order.
		targetSnaps, err := VolumeDBSnapshotsGet(b, projectName, dbVol.Name, drivers.VolumeTypeCustom)
		if err != nil {
			return err
		}

		targetSnapshots := make([]drivers.Volume, 0, len(targetSnaps))
		for _, targetSnap := range targetSnaps {
			snapshotStorageName := project.StorageVolume(projectName, targetSnap.Name)
			targetSnapshots = append(targetSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, targetSnap.Config))
		}

		volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)
		srcVolCopy := drivers.NewVolumeCopy(srcVol, sourceSnapshots...)

		err = b.driver.RefreshVolume(volCopy, srcVolCopy, srcSnapVols, false, op)
		if err != nil {
			return err
		}
	} else {
		l.Debug("RefreshCustomVolume cross-pool mode detected")

		// Negotiate the migration type to use.
		offeredTypes := srcPool.MigrationTypes(contentType, true, snapshots)
		offerHeader := migration.TypesToHeader(offeredTypes...)
		migrationTypes, err := migration.MatchTypes(offerHeader, FallbackMigrationType(contentType), b.MigrationTypes(contentType, true, snapshots))
		if err != nil {
			return fmt.Errorf("Failed to negotiate copy migration type: %w", err)
		}

		var volSize int64

		if contentType == drivers.ContentTypeBlock {
			err = srcVol.MountTask(func(_ string, _ *operations.Operation) error {
				srcPoolBackend, ok := srcPool.(*lxdBackend)
				if !ok {
					return fmt.Errorf("Pool is not a lxdBackend")
				}

				volDiskPath, err := srcPoolBackend.driver.GetVolumeDiskPath(srcVol)
				if err != nil {
					return err
				}

				volSize, err = block.DiskSizeBytes(volDiskPath)
				if err != nil {
					return err
				}

				return nil
			}, nil)
			if err != nil {
				return err
			}
		}

		ctx, cancel := context.WithCancel(context.Background())

		// Use in-memory pipe pair to simulate a connection between the sender and receiver.
		aEnd, bEnd := memorypipe.NewPipePair(ctx)

		// Run sender and receiver in separate go routines to prevent deadlocks.
		aEndErrCh := make(chan error, 1)
		bEndErrCh := make(chan error, 1)
		go func() {
			err := srcPool.MigrateCustomVolume(srcProjectName, aEnd, &migration.VolumeSourceArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               srcConfig.Volume.Name,
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				TrackProgress:      true, // Do use a progress tracker on sender.
				ContentType:        string(contentType),
				Info:               &migration.Info{Config: srcConfig},
			}, op)

			if err != nil {
				cancel()
			}

			aEndErrCh <- err
		}()

		go func() {
			err := b.CreateCustomVolumeFromMigration(projectName, bEnd, migration.VolumeTargetArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               dbVol.Name,
				Description:        desc,
				Config:             config,
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				TrackProgress:      false, // Do not use a progress tracker on receiver.
				ContentType:        string(contentType),
				VolumeSize:         volSize, // Block size setting override.
				Refresh:            true,
			}, op)

			if err != nil {
				cancel()
			}

			bEndErrCh <- err
		}()

		// Capture errors from the sender and receiver from their result channels.
		errs := []error{}
		aEndErr := <-aEndErrCh
		if aEndErr != nil {
			_ = aEnd.Close()
			errs = append(errs, aEndErr)
		}

		bEndErr := <-bEndErrCh
		if bEndErr != nil {
			errs = append(errs, bEndErr)
		}

		cancel()

		if len(errs) > 0 {
			return fmt.Errorf("Refresh custom volume from copy failed: %v", errs)
		}
	}

	revert.Success()
	return nil
}

// RefreshInstance synchronises one instance's volume (and optionally snapshots) over another.
// Snapshots that are not present in the source but are in the destination are removed from the
// destination if snapshots are included in the synchronisation. An empty srcSnapshots argument
// indicates a volume-only refresh.
func (b *lxdBackend) RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "src": src.Name(), "srcSnapshots": len(srcSnapshots)})
	l.Debug("RefreshInstance started")
	defer l.Debug("RefreshInstance finished")

	// This indicates whether or not it's a volume-only refresh.
	snapshots := len(srcSnapshots) > 0

	if inst.Type() != src.Type() {
		return fmt.Errorf("Instance types must match")
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Get the source storage pool.
	srcPool, err := LoadByInstance(b.state, src)
	if err != nil {
		return err
	}

	srcPoolBackend, ok := srcPool.(*lxdBackend)
	if !ok {
		return fmt.Errorf("Source pool is not a lxdBackend")
	}

	// Check source volume exists, and get its config including all of the snapshots.
	srcConfig, err := srcPool.GenerateInstanceBackupConfig(src, true, op)
	if err != nil {
		return fmt.Errorf("Failed generating instance refresh config: %w", err)
	}

	// Ensure that only the requested snapshots are included in the source config.
	allSnapshots := srcConfig.VolumeSnapshots
	sourceSnapshots := make([]drivers.Volume, 0, len(srcConfig.VolumeSnapshots))
	srcConfig.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(srcSnapshots))
	for i := range allSnapshots {
		snapshotName := drivers.GetSnapshotVolumeName(src.Name(), allSnapshots[i].Name)
		snapshotStorageName := project.Instance(src.Project().Name, snapshotName)

		// Use the information from the backup config to create a list of all the source volume's snapshots.
		// This way we don't have to retrieve them separately from the database.
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, allSnapshots[i].Config))

		found := false
		for _, srcSnapshot := range srcSnapshots {
			_, srcSnapshotName, _ := api.GetParentAndSnapshotName(srcSnapshot.Name())
			if srcSnapshotName == allSnapshots[i].Name {
				found = true
				break
			}
		}

		if found {
			srcConfig.VolumeSnapshots = append(srcConfig.VolumeSnapshots, allSnapshots[i])
		}
	}

	// Unset the snapshots in the backup config if not requested by the caller.
	// Those were only required to create the list of source volume snapshots.
	if !snapshots {
		srcConfig.Snapshots = nil
		srcConfig.VolumeSnapshots = nil
	}

	// Get source volume construct.
	srcVolStorageName := project.Instance(src.Project().Name, src.Name())
	srcVol := b.GetVolume(volType, contentType, srcVolStorageName, srcConfig.Volume.Config)

	// Get source snapshot volume constructs.
	snapshotNames := make([]string, 0, len(srcConfig.VolumeSnapshots))
	for i := range srcConfig.VolumeSnapshots {
		snapshotNames = append(snapshotNames, srcConfig.VolumeSnapshots[i].Name)
	}

	revert := revert.New()
	defer revert.Fail()

	// Some driver backing stores require that running instances be frozen during copy.
	if !src.IsSnapshot() && srcPoolBackend.driver.Info().RunningCopyFreeze && src.IsRunning() && !src.IsFrozen() && !allowInconsistent {
		b.logger.Info("Freezing instance for consistent refresh")
		err = src.Freeze()
		if err != nil {
			return err
		}

		defer func() { _ = src.Unfreeze() }()

		// Attempt to sync the filesystem.
		_ = filesystem.SyncFS(src.RootfsPath())
	}

	if b.Name() == srcPool.Name() {
		l.Debug("RefreshInstance same-pool mode detected")

		// Create database entries for new storage volume snapshots.
		for i := range srcConfig.VolumeSnapshots {
			newSnapshotName := drivers.GetSnapshotVolumeName(inst.Name(), srcConfig.VolumeSnapshots[i].Name)

			var volumeSnapExpiryDate time.Time
			if srcConfig.VolumeSnapshots[i].ExpiresAt != nil {
				volumeSnapExpiryDate = *srcConfig.VolumeSnapshots[i].ExpiresAt
			}

			// Create a new snapshot volume with its own config and UUID.
			snapVol := b.GetNewVolume(volType, contentType, newSnapshotName, srcConfig.VolumeSnapshots[i].Config)

			// Validate config and create database entry for new storage volume.
			err = VolumeDBCreate(b, inst.Project().Name, newSnapshotName, srcConfig.VolumeSnapshots[i].Description, volType, true, snapVol.Config(), srcConfig.VolumeSnapshots[i].CreatedAt, volumeSnapExpiryDate, contentType, false, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, newSnapshotName, volType) })
		}

		// Get all of the target instance's snapshots.
		// Afterwards load the volume from the snapshot to ensure the right ordering.
		instSnapshots, err := inst.Snapshots()
		if err != nil {
			return err
		}

		targetSnapshots := make([]drivers.Volume, 0, len(instSnapshots))
		for _, instSnapshot := range instSnapshots {
			snap, err := VolumeDBGet(b, inst.Project().Name, instSnapshot.Name(), volType)
			if err != nil {
				return err
			}

			snapshotStorageName := project.Instance(inst.Project().Name, snap.Name)
			targetSnapshots = append(targetSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, snap.Config))
		}

		volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)
		srcVolCopy := drivers.NewVolumeCopy(srcVol, sourceSnapshots...)

		err = b.driver.RefreshVolume(volCopy, srcVolCopy, snapshotNames, allowInconsistent, op)
		if err != nil {
			return err
		}
	} else {
		// We are copying volumes between storage pools so use migration system as it will
		// be able to negotiate a common transfer method between pool types.
		l.Debug("RefreshInstance cross-pool mode detected")

		// Negotiate the migration type to use.
		offeredTypes := srcPool.MigrationTypes(contentType, true, snapshots)
		offerHeader := migration.TypesToHeader(offeredTypes...)
		migrationTypes, err := migration.MatchTypes(offerHeader, FallbackMigrationType(contentType), b.MigrationTypes(contentType, true, snapshots))
		if err != nil {
			return fmt.Errorf("Failed to negotiate copy migration type: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Run sender and receiver in separate go routines to prevent deadlocks.
		g, ctx := errgroup.WithContext(ctx)

		// Use in-memory pipe pair to simulate a connection between the sender and receiver.
		// Use context from error group so that if either side fails the pipes are closed.
		aEnd, bEnd := memorypipe.NewPipePair(ctx)

		// Start each side of the migration concurrently and collect any errors.
		g.Go(func() error {
			return srcPool.MigrateInstance(src, aEnd, &migration.VolumeSourceArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               src.Name(),
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				TrackProgress:      true, // Do use a progress tracker on sender.
				AllowInconsistent:  allowInconsistent,
				Refresh:            true, // Indicate to sender to use incremental streams.
				Info:               &migration.Info{Config: srcConfig},
				VolumeOnly:         !snapshots,
			}, op)
		})

		g.Go(func() error {
			return b.CreateInstanceFromMigration(inst, bEnd, migration.VolumeTargetArgs{
				IndexHeaderVersion: migration.IndexHeaderVersion,
				Name:               inst.Name(),
				Snapshots:          snapshotNames,
				MigrationType:      migrationTypes[0],
				Refresh:            true,  // Indicate to receiver volume should exist.
				TrackProgress:      false, // Do not use a progress tracker on receiver.
				VolumeOnly:         !snapshots,
			}, op)
		})

		err = g.Wait()
		if err != nil {
			return fmt.Errorf("Create instance volume from copy failed: %w", err)
		}
	}

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	err = inst.DeferTemplateApply(instance.TemplateTriggerCopy)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// imageFiller returns a function that can be used as a filler function with CreateVolume().
// The function returned will unpack the specified image archive into the specified mount path
// provided, and for VM images, a raw root block path is required to unpack the qcow2 image into.
func (b *lxdBackend) imageFiller(fingerprint string, op *operations.Operation) func(vol drivers.Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) {
	return func(vol drivers.Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) {
		var tracker *ioprogress.ProgressTracker
		if op != nil { // Not passed when being done as part of pre-migration setup.
			metadata := make(map[string]any)
			tracker = &ioprogress.ProgressTracker{
				Handler: func(percent, speed int64) {
					shared.SetProgressMetadata(metadata, "create_instance_from_image_unpack", "Unpacking image", percent, 0, speed)
					_ = op.UpdateMetadata(metadata)
				}}
		}

		imageFile := shared.VarPath("images", fingerprint)
		return ImageUnpack(imageFile, vol, rootBlockPath, b.state.OS, allowUnsafeResize, tracker)
	}
}

// isoFiller returns a function that can be used as a filler function with CreateVolume().
// The function returned will copy the ISO content into the specified mount path
// provided.
func (b *lxdBackend) isoFiller(data io.Reader) func(vol drivers.Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) {
	return func(_ drivers.Volume, rootBlockPath string, _ bool) (int64, error) {
		f, err := os.OpenFile(rootBlockPath, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return -1, err
		}

		defer func() { _ = f.Close() }()

		return io.Copy(f, data)
	}
}

// imageConversionFiller returns a function that converts an image from the given path to the instance's volume.
// Function returns the unpacked image size on success. Otherwise, it returns -1 for size and an error.
func (b *lxdBackend) imageConversionFiller(imgPath string, imgFormat string, op *operations.Operation) func(vol drivers.Volume, rootBlockPath string, allowUnsafeResize bool) (sizeInBytes int64, err error) {
	return func(vol drivers.Volume, _ string, _ bool) (int64, error) {
		diskPath, err := b.driver.GetVolumeDiskPath(vol)
		if err != nil {
			return -1, fmt.Errorf("Failed getting instance volume disk path: %v", err)
		}

		// Ensure conversion supports the uploaded image format.
		supportedImageFormats := []string{"qcow", "qcow2", "raw", "vdi", "vhdx", "vmdk"}
		if !shared.ValueInSlice(imgFormat, supportedImageFormats) {
			return -1, fmt.Errorf("Unsupported image format %q, allowed formats are [%s]", imgFormat, strings.Join(supportedImageFormats, ", "))
		}

		// Setup the progress tracker.
		var tracker *ioprogress.ProgressTracker
		if op != nil {
			metadata := make(map[string]any)
			tracker = &ioprogress.ProgressTracker{
				Handler: func(percent, speed int64) {
					displayPrefix := "Converting image format from " + imgFormat + " to raw"
					shared.SetProgressMetadata(metadata, "format_progress", displayPrefix, percent, 0, speed)
					_ = op.UpdateMetadata(metadata)
				},
			}
		}

		// Convert uploaded image from backups directory into RAW format on the instance volume.
		cmd := []string{
			// Run with low priority to reduce CPU impact on other processes.
			"nice", "-n19",
			"qemu-img", "convert", "-p", "-f", imgFormat, "-O", "raw", imgPath, diskPath, "-t", "writeback",
		}

		// Check for Direct I/O support.
		from, err := os.OpenFile(imgPath, unix.O_DIRECT|unix.O_RDONLY, 0)
		if err == nil {
			cmd = append(cmd, "-T", "none")
			_ = from.Close()
		}

		to, err := os.OpenFile(diskPath, unix.O_DIRECT|unix.O_WRONLY, 0)
		if err == nil {
			cmd = append(cmd, "-t", "none")
			_ = to.Close()
		}

		b.logger.Debug("Image conversion started", logger.Ctx{"from": imgFormat, "to": "raw"})
		defer b.logger.Debug("Image conversion finished", logger.Ctx{"from": imgFormat, "to": "raw"})

		out, err := apparmor.QemuImg(b.state.OS, cmd, imgPath, diskPath, tracker)
		if err != nil {
			b.logger.Debug("Image conversion failed", logger.Ctx{"error": out})
			return -1, fmt.Errorf("qemu-img convert: failed to convert image from %q to %q format: %v", imgFormat, "raw", err)
		}

		// Remove the image after the conversion to free up the space as soon as possible.
		err = os.Remove(imgPath)
		if err != nil {
			return -1, err
		}

		// Convert volume size to bytes.
		volSizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return -1, err
		}

		return volSizeBytes, nil
	}
}

// recvVolumeFiller returns a function that receives the instance's volume.
// Function returns the volume size on success. Otherwise, it returns -1 for size and an error.
func (b *lxdBackend) recvVolumeFiller(conn io.ReadWriteCloser, contentType drivers.ContentType, args migration.VolumeTargetArgs, op *operations.Operation) func(vol drivers.Volume, rootBlockPath string, allowUnsafeResize bool) (sizeInBytes int64, err error) {
	return func(vol drivers.Volume, rootBlockPath string, _ bool) (int64, error) {
		if contentType == drivers.ContentTypeFS {
			// Receive filesystem.
			err := b.recvFS(vol.MountPath(), vol.Name(), conn, args, op)
			if err != nil {
				return -1, err
			}
		} else {
			// Receive block volume.
			to, err := os.OpenFile(rootBlockPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
			if err != nil {
				return -1, fmt.Errorf("Error opening file for writing %q: %w", rootBlockPath, err)
			}

			defer func() { _ = to.Close() }()

			err = b.recvBlockVol(to, vol.Name(), conn, args, op)
			if err != nil {
				return -1, err
			}
		}

		// Convert volume size to bytes.
		volSizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return -1, err
		}

		return volSizeBytes, nil
	}
}

func (b *lxdBackend) recvBlockVol(toFile *os.File, volName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	b.logger.Debug("Receive block volume started", logger.Ctx{"volName": volName})
	defer b.logger.Debug("Receive block volume finished", logger.Ctx{"volName": volName})

	var wrapper *ioprogress.ProgressTracker
	if args.TrackProgress {
		wrapper = migration.ProgressTracker(op, "block_progress", "Transferring instance")
	}

	// Setup progress tracker.
	fromPipe := io.ReadCloser(conn)
	if wrapper != nil {
		fromPipe = &ioprogress.ProgressReader{
			ReadCloser: fromPipe,
			Tracker:    wrapper,
		}
	}

	_, err := io.Copy(toFile, fromPipe)
	if err != nil {
		return fmt.Errorf("Error copying from migration connection to %q: %w", toFile.Name(), err)
	}

	// Reset the file's read pointer to the beginning, otherwise we cannot read the tar's header.
	_, err = toFile.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}

	// Ensure that the received file is not a tarball, which is also the case for OVA format.
	_, err = tar.NewReader(toFile).Next()
	if err == nil {
		return fmt.Errorf("Instance cannot be imported from a tar archive or OVA file")
	}

	return toFile.Close()
}

func (b *lxdBackend) recvFS(path string, volName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	b.logger.Debug("Receiving filesystem volume started", logger.Ctx{"volName": volName, "path": path, "features": args.MigrationType.Features})
	defer b.logger.Debug("Receiving filesystem volume stopped", logger.Ctx{"volName": volName, "path": path})

	var wrapper *ioprogress.ProgressTracker
	if args.TrackProgress {
		wrapper = migration.ProgressTracker(op, "fs_progress", "Transferring instance")
	}

	return rsync.Recv(shared.AddSlash(path), conn, wrapper, args.MigrationType.Features)
}

// CreateInstanceFromImage creates a new volume for an instance populated with the image requested.
// On failure caller is expected to call DeleteInstance() to clean up.
func (b *lxdBackend) CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("CreateInstanceFromImage started")
	defer l.Debug("CreateInstanceFromImage finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	revert := revert.New()
	defer revert.Fail()

	volumeConfig := make(map[string]string)
	err = b.applyInstanceRootDiskInitialValues(inst, volumeConfig)
	if err != nil {
		return err
	}

	// Determine whether an optimized image should be used.
	useOptimizedImage, err := b.shouldUseOptimizedImage(fingerprint, contentType, volumeConfig)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Perform this after checking for optimized image as overwriting the volumes UUID
	// will cause a non matching configuration which will always fall back to non optimized storage.
	vol := b.GetNewVolume(volType, contentType, volStorageName, volumeConfig)

	// Set the parent volume UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, inst.Project().Name)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), "", volType, false, vol.Config(), inst.CreationDate(), time.Time{}, contentType, true, false)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Leave reverting on failure to caller, they are expected to call DeleteInstance().

	// If the driver doesn't support optimized image volumes or the optimized image volume should not be used,
	// create a new empty volume and populate it with the contents of the image archive.
	if !useOptimizedImage {
		volFiller := drivers.VolumeFiller{
			Fingerprint: fingerprint,
			Fill:        b.imageFiller(fingerprint, op),
		}

		err = b.driver.CreateVolume(vol, &volFiller, op)
		if err != nil {
			return err
		}
	} else {
		// If the driver supports optimized images then ensure the optimized image volume has been created
		// for the images's fingerprint and that it matches the pool's current volume settings, and if not
		// recreating using the pool's current volume settings.
		err = b.EnsureImage(fingerprint, op)
		if err != nil {
			return err
		}

		// Try and load existing volume config on this storage pool so we can compare filesystems if needed.
		imgDBVol, err := VolumeDBGet(b, api.ProjectDefaultName, fingerprint, drivers.VolumeTypeImage)
		if err != nil {
			return err
		}

		imgVol := b.GetVolume(drivers.VolumeTypeImage, contentType, fingerprint, imgDBVol.Config)

		// Derive the volume size to use for a new volume when copying from a source volume.
		// Where possible (if the source volume has a volatile.rootfs.size property), it checks that the
		// source volume isn't larger than the volume's "size" and the pool's "volume.size" setting.
		l.Debug("Checking volume size")
		newVolSize, err := vol.ConfigSizeFromSource(imgVol)
		if err != nil {
			return err
		}

		// Set the derived size directly as the "size" property on the new volume so that it is applied.
		vol.SetConfigSize(newVolSize)
		l.Debug("Set new volume size", logger.Ctx{"size": newVolSize})

		volCopy := drivers.NewVolumeCopy(vol)
		imgVolCopy := drivers.NewVolumeCopy(imgVol)

		// Proceed to create a new volume by copying the optimized image volume.
		err = b.driver.CreateVolumeFromCopy(volCopy, imgVolCopy, false, op)

		// If the driver returns ErrCannotBeShrunk, this means that the cached volume that the new volume
		// is to be created from is larger than the requested new volume size, and cannot be shrunk.
		// So we unpack the image directly into a new volume rather than use the optimized snapsot.
		// This is slower but allows for individual volumes to be created from an image that are smaller
		// than the pool's volume settings.
		if errors.Is(err, drivers.ErrCannotBeShrunk) {
			l.Debug("Cached image volume is larger than new volume and cannot be shrunk, creating non-optimized volume")

			volFiller := drivers.VolumeFiller{
				Fingerprint: fingerprint,
				Fill:        b.imageFiller(fingerprint, op),
			}

			err = b.driver.CreateVolume(vol, &volFiller, op)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	err = inst.DeferTemplateApply(instance.TemplateTriggerCreate)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// CreateInstanceFromMigration receives an instance being migrated.
// The args.Name and args.Config fields are ignored and, instance properties are used instead.
func (b *lxdBackend) CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "args": fmt.Sprintf("%+v", args)})
	l.Debug("CreateInstanceFromMigration started")
	defer l.Debug("CreateInstanceFromMigration finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if args.Config != nil {
		return fmt.Errorf("Migration VolumeTargetArgs.Config cannot be set for instances")
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Receive index header from source if applicable and respond confirming receipt.
	// This will also communicate the args.Refresh setting back to the source (in case it was changed by the
	// caller if the instance DB record already exists).
	srcInfo, err := b.migrationIndexHeaderReceive(l, args.IndexHeaderVersion, conn, args.Refresh)
	if err != nil {
		return err
	}

	var volumeDescription string
	var volumeConfig map[string]string

	// Check if the volume exists in database
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	// Prefer using existing volume config (to allow mounting existing volume correctly).
	if dbVol != nil {
		volumeConfig = dbVol.Config
		volumeDescription = dbVol.Description
	} else if srcInfo != nil && srcInfo.Config != nil && srcInfo.Config.Volume != nil {
		volumeConfig = srcInfo.Config.Volume.Config
		volumeDescription = srcInfo.Config.Volume.Description
	} else {
		volumeConfig = make(map[string]string)
		volumeDescription = args.Description
	}

	isRemoteClusterMove := args.ClusterMoveSourceName != "" && b.driver.Info().Remote

	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	var vol drivers.Volume
	if isRemoteClusterMove || args.Refresh {
		// In case it's a cluster move don't instantiate a new volume.
		// Instead load the existing volume and config from the database.
		vol = b.GetVolume(volType, contentType, volStorageName, volumeConfig)
	} else {
		vol = b.GetNewVolume(volType, contentType, volStorageName, volumeConfig)
	}

	// Ensure storage volume settings are honored when doing migration.
	// This is only done for non-optimized migration because some storage volume settings,
	// in particular block mode, cannot be honored when doing optimized migration.
	if args.MigrationType.FSType == migration.MigrationFSType_RSYNC || args.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		vol.SetHasSource(false)

		err = b.driver.FillVolumeConfig(vol)
		if err != nil {
			return fmt.Errorf("Failed filling volume config: %w", err)
		}
	}

	// Check if the volume exists on storage.
	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	// Check for inconsistencies between database and storage before continuing.
	if dbVol == nil && volExists {
		return fmt.Errorf("Volume already exists on storage but not in database")
	}

	if dbVol != nil && !volExists {
		return fmt.Errorf("Volume exists in database but not on storage")
	}

	// Consistency check for refresh mode.
	// We expect that the args.Refresh setting will have already been set to false by the caller as part of
	// detecting if the instance DB record exists or not. If we get here then something has gone wrong.
	if args.Refresh && !volExists {
		return fmt.Errorf("Cannot refresh volume, doesn't exist on migration target storage")
	}

	revert := revert.New()
	defer revert.Fail()

	if !args.Refresh {
		if volExists {
			if !isRemoteClusterMove {
				return fmt.Errorf("Cannot create volume, already exists on migration target storage")
			}
		} else {
			// Validate config and create database entry for new storage volume if not refreshing.
			// Strip unsupported config keys (in case the export was made from a different type of storage pool).
			err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), volumeDescription, volType, false, vol.Config(), inst.CreationDate(), time.Time{}, contentType, true, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })
		}
	}

	if !isRemoteClusterMove {
		for i, snapName := range args.Snapshots {
			newSnapshotName := drivers.GetSnapshotVolumeName(inst.Name(), snapName)
			snapConfig := vol.Config()           // Use parent volume config by default.
			snapDescription := volumeDescription // Use parent volume description by default.
			snapExpiryDate := time.Time{}
			snapCreationDate := time.Time{}

			// If the source snapshot config is available, use that.
			if srcInfo != nil && srcInfo.Config != nil {
				if len(srcInfo.Config.Snapshots) >= i-1 && srcInfo.Config.Snapshots[i] != nil && srcInfo.Config.Snapshots[i].Name == snapName {
					// Use instance snapshot's creation date if snap info available.
					snapCreationDate = srcInfo.Config.Snapshots[i].CreatedAt
				}

				if len(srcInfo.Config.VolumeSnapshots) >= i-1 && srcInfo.Config.VolumeSnapshots[i] != nil && srcInfo.Config.VolumeSnapshots[i].Name == snapName {
					// Check if snapshot volume config is available then use it.
					snapDescription = srcInfo.Config.VolumeSnapshots[i].Description
					snapConfig = srcInfo.Config.VolumeSnapshots[i].Config

					if srcInfo.Config.VolumeSnapshots[i].ExpiresAt != nil {
						snapExpiryDate = *srcInfo.Config.VolumeSnapshots[i].ExpiresAt
					}

					// Use volume's creation date if available.
					if !srcInfo.Config.VolumeSnapshots[i].CreatedAt.IsZero() {
						snapCreationDate = srcInfo.Config.VolumeSnapshots[i].CreatedAt
					}
				}
			}

			// Create a new snapshot volume with its own config and UUID.
			snapVol := b.GetNewVolume(volType, contentType, newSnapshotName, snapConfig)

			// Validate config and create database entry for new storage volume.
			// Strip unsupported config keys (in case the export was made from a different type of storage pool).
			err = VolumeDBCreate(b, inst.Project().Name, newSnapshotName, snapDescription, volType, true, snapVol.Config(), snapCreationDate, snapExpiryDate, contentType, true, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, newSnapshotName, volType) })
		}
	}

	// Generate the effective root device volume for instance.
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Override args.Name and args.Config to ensure volume is created based on instance.
	args.Config = vol.Config()
	args.Name = inst.Name()

	projectName := inst.Project().Name

	// If migration header supplies a volume size, then use that as block volume size instead of pool default.
	// This way if the volume being received is larger than the pool default size, the block volume created
	// will still be able to accommodate it.
	if args.VolumeSize > 0 && contentType == drivers.ContentTypeBlock {
		l.Debug("Setting volume size from offer header", logger.Ctx{"size": args.VolumeSize})
		args.Config["size"] = strconv.FormatInt(args.VolumeSize, 10)
	} else if args.Config["size"] != "" {
		l.Debug("Using volume size from root disk config", logger.Ctx{"size": args.Config["size"]})
	}

	var preFiller drivers.VolumeFiller

	if !args.Refresh && !isRemoteClusterMove {
		// If the negotiated migration method is rsync and the instance's base image is
		// already on the host then setup a pre-filler that will unpack the local image
		// to try and speed up the rsync of the incoming volume by avoiding the need to
		// transfer the base image files too.
		if args.MigrationType.FSType == migration.MigrationFSType_RSYNC {
			fingerprint := inst.ExpandedConfig()["volatile.base_image"]
			imageExists := false

			if fingerprint != "" {
				err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Confirm that the image is present in the project.
					_, _, err = tx.GetImage(ctx, fingerprint, cluster.ImageFilter{Project: &projectName})

					return err
				})
				if err != nil && !response.IsNotFoundError(err) {
					return err
				}

				// Make sure that the image is available locally too (not guaranteed in clusters).
				imageExists = err == nil && shared.PathExists(shared.VarPath("images", fingerprint))
			}

			if imageExists {
				l.Debug("Using optimised migration from existing image", logger.Ctx{"fingerprint": fingerprint})

				// Populate the volume filler with the fingerprint and image filler
				// function that can be used by the driver to pre-populate the
				// volume with the contents of the image.
				preFiller = drivers.VolumeFiller{
					Fingerprint: fingerprint,
					Fill:        b.imageFiller(fingerprint, op),
				}

				// Ensure if the image doesn't yet exist on a driver which supports
				// optimized storage, then it gets created first.
				err = b.EnsureImage(preFiller.Fingerprint, op)
				if err != nil {
					return err
				}
			}
		}
	}

	// Retrieve a list of target volume snapshots.
	// Afterwards load the volume from the snapshot to ensure the right ordering.
	instSnapshots, err := inst.Snapshots()
	if err != nil {
		return err
	}

	targetSnapshots := make([]drivers.Volume, 0, len(instSnapshots))
	for _, instSnapshot := range instSnapshots {
		snap, err := VolumeDBGet(b, inst.Project().Name, instSnapshot.Name(), volType)
		if err != nil {
			return err
		}

		snapshotStorageName := project.Instance(inst.Project().Name, instSnapshot.Name())
		targetSnapshots = append(targetSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, snap.Config))
	}

	// Set the parent volume's UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, projectName)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)

	err = b.driver.CreateVolumeFromMigration(volCopy, conn, args, &preFiller, op)
	if err != nil {
		return err
	}

	if !isRemoteClusterMove {
		revert.Add(func() { _ = b.DeleteInstance(inst, op) })
	}

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	if len(args.Snapshots) > 0 {
		err = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, inst.Name())
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// CreateInstanceFromConversion receives a disk or filesystem and creates and instance from it.
// Based on the provided conversion options, the received disk is converted into the raw format
// and/or the virtio drivers are injected into it.
func (b *lxdBackend) CreateInstanceFromConversion(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "args": fmt.Sprintf("%+v", args)})
	l.Debug("CreateInstanceFromConversion started")
	defer l.Debug("CreateInstanceFromConversion finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if args.Config != nil {
		return fmt.Errorf("VolumeTargetArgs.Config cannot be set for conversion")
	}

	if args.Refresh {
		return fmt.Errorf("Volume cannot be refreshed during conversion")
	}

	if len(args.Snapshots) > 0 {
		return fmt.Errorf("Snapshots cannot be received during conversion")
	}

	isRemoteClusterMove := args.ClusterMoveSourceName != "" && b.driver.Info().Remote
	if isRemoteClusterMove {
		return fmt.Errorf("Conversion cannot be used for moving instances between members")
	}

	contentType := InstanceContentType(inst)
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	volConfig := make(map[string]string)
	vol := b.GetNewVolume(volType, contentType, volStorageName, volConfig)

	// Ensure storage volume settings are honored when doing conversion.
	vol.SetHasSource(false)
	err = b.driver.FillVolumeConfig(vol)
	if err != nil {
		return fmt.Errorf("Failed filling volume config: %w", err)
	}

	// Check if the volume exists in database
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	if dbVol != nil {
		return fmt.Errorf("Volume for instance %q already exists in database", inst.Name())
	}

	// Check if the volume exists on storage.
	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		return fmt.Errorf("Volume already exists on storage but not in database")
	}

	revert := revert.New()
	defer revert.Fail()

	// Validate config and create database entry for new storage volume if not refreshing.
	// Strip unsupported config keys (in case the export was made from a different type of storage pool).
	err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), args.Description, volType, false, vol.Config(), inst.CreationDate(), time.Time{}, contentType, false, true)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

	// Generate the effective root device volume for instance.
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Get instance's root disk device from local devices. Do not use expanded devices, as we want
	// to determine whether the root disk volume size was explicitly set by the client.
	canResizeRootDiskSize := true
	_, rootDiskConf, err := instancetype.GetRootDiskDevice(inst.LocalDevices().CloneNative())
	if err == nil && rootDiskConf != nil && rootDiskConf["size"] != "" {
		// User has explicitly configured the root disk device. Therefore, we should not mess
		// with the root disk configuration.
		canResizeRootDiskSize = false
	}

	var srcDiskSize int64
	var volFiller drivers.VolumeFiller

	if slices.Contains(args.ConversionOptions, "format") {
		// When conversion option "format" is enabled, we need to upload the image
		// to a temporary location before converting it into the desired format.
		// The conversion cannot be done in-place, therefore the image has to be
		// saved in an intermediate location.
		conversionID := "conversion_" + inst.Project().Name + "_" + inst.Name()
		imgPath := filepath.Join(shared.VarPath("backups"), conversionID)

		// Create new file in backups directory.
		to, err := os.OpenFile(imgPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("Error opening file for writing %q: %w", imgPath, err)
		}

		// Ensure temporary image in backups directory is removed regardless of the conversion success.
		defer func() {
			_ = to.Close()
			_ = os.Remove(imgPath)
		}()

		// Receive the image for conversion.
		err = b.recvBlockVol(to, vol.Name(), conn, args, op)
		if err != nil {
			return err
		}

		// Extract image format and size.
		imgFormat, imgBytes, err := qemuImageInfo(b.state.OS, imgPath, nil)
		if err != nil {
			return err
		}

		srcDiskSize = imgBytes

		if canResizeRootDiskSize {
			// Set size of the volume to the uncompressed image size.
			l.Debug("Setting volume size to uncompressed image size", logger.Ctx{"size": imgBytes})
			vol.SetConfigSize(strconv.FormatInt(imgBytes, 10))
		}

		// Convert received image into intance volume.
		volFiller.Fill = b.imageConversionFiller(imgPath, imgFormat, op)
	} else {
		// If volume size is provided, then use that as block volume size instead of pool default.
		// This way if the volume being received is larger than the pool default size, the created
		// block volume will still be able to accommodate it.
		if canResizeRootDiskSize && contentType == drivers.ContentTypeBlock && args.VolumeSize > 0 {
			l.Debug("Setting volume size to source disk size", logger.Ctx{"size": args.VolumeSize})
			vol.SetConfigSize(strconv.FormatInt(args.VolumeSize, 10))
		}

		srcDiskSize = args.VolumeSize

		// If formatting is not required, receive the volume (block / FS) directly
		// into the instance volume.
		volFiller.Fill = b.recvVolumeFiller(conn, contentType, args, op)
	}

	// Parse volume size into bytes.
	volBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return fmt.Errorf("Failed parsing instance volume size")
	}

	// Parse source disk size into bytes.
	srcSize, err := units.ParseByteSizeString(strconv.FormatInt(srcDiskSize, 10))
	if err != nil {
		return fmt.Errorf("Failed parsing source disk size")
	}

	// Ensure source disk will fit into the instance volume.
	if volBytes < srcSize {
		// Convert to IEC format for nicer error.
		imgSize := units.GetByteSizeStringIEC(srcSize, 2)
		volSize := units.GetByteSizeStringIEC(volBytes, 2)
		return fmt.Errorf("Volume size (%s) is lower then source disk size (%s)", volSize, imgSize)
	}

	err = b.driver.CreateVolume(vol, &volFiller, op)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = b.driver.DeleteVolume(vol, op) })

	// At this point, the instance's volume is populated. If "virtio" option is enabled,
	// inject the virtio drivers.
	if slices.Contains(args.ConversionOptions, "virtio") {
		b.logger.Debug("Inject virtio drivers started")
		defer b.logger.Debug("Inject virtio drivers finished")

		err = b.driver.MountVolume(vol, op)
		if err != nil {
			return err
		}

		defer func() { _, _ = b.driver.UnmountVolume(vol, true, op) }()

		diskPath, err := b.driver.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		out, err := exec.Command("virt-v2v-in-place", "--version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to get virt-v2v-in-place version: %w (%s)", err, string(out))
		}

		// Extract virt-v2v-in-place version (format is "virt-v2v-in-place 1.2.3").
		v2vVersionParts := strings.Split(strings.TrimSpace(string(out)), " ")
		v2vVersion, err := version.NewDottedVersion(v2vVersionParts[len(v2vVersionParts)-1])
		if err != nil {
			return err
		}

		minVersion, err := version.NewDottedVersion("2.3.4")
		if err != nil {
			return err
		}

		// Ensure virt-v2v-in-place version is higher then or equal to the minimum required version.
		if v2vVersion.Compare(minVersion) < 0 {
			return fmt.Errorf("The virt-v2v-in-place version %q does not match the minimum required version %q", v2vVersion, minVersion)
		}

		// Run virt-v2v-in-place to inject virtio drivers.
		cmd := exec.Command(
			// Run with low priority to reduce the CPU impact on other processes.
			"nice", "-n19",
			"virt-v2v-in-place", "-i", "disk", "-if", "raw", "--block-driver", "virtio-scsi", diskPath,
		)

		// Instruct virt-v2v-in-place where to search for windows drivers.
		cmd.Env = append(os.Environ(),
			"VIRTIO_WIN=/usr/share/virtio-win/virtio-win.iso",
			"VIRT_TOOLS_DATA_DIR=/usr/share/virt-tools",
		)

		out, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to inject virtio drivers: %w (%s)", err, string(out))
		}
	}

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// RenameInstance renames the instance's root volume and any snapshot volumes.
func (b *lxdBackend) RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "newName": newName})
	l.Debug("RenameInstance started")
	defer l.Debug("RenameInstance finished")

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance cannot be a snapshot")
	}

	if shared.IsSnapshot(newName) {
		return fmt.Errorf("New name cannot be a snapshot")
	}

	// Quick checks.
	err := instancetype.ValidName(newName, false)
	if err != nil {
		return err
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	volDBType, err := VolumeTypeToDBType(volType)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	volume, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	var snapshots []string

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get any snapshots the instance has in the format <instance name>/<snapshot name>.
		snapshots, err = tx.GetInstanceSnapshotsNames(ctx, inst.Project().Name, inst.Name())

		return err
	})
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		revert.Add(func() {
			_ = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, newName)
			_ = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, inst.Name())
		})
	}

	// Rename each snapshot DB record to have the new parent volume prefix.
	for _, srcSnapshot := range snapshots {
		_, snapName, _ := api.GetParentAndSnapshotName(srcSnapshot)
		newSnapVolName := drivers.GetSnapshotVolumeName(newName, snapName)

		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, srcSnapshot, newSnapVolName, volDBType, b.ID())
		})
		if err != nil {
			return err
		}

		revert.Add(func() {
			_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, newSnapVolName, srcSnapshot, volDBType, b.ID())
			})
		})
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Rename the parent volume DB record.
		return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, inst.Name(), newName, volDBType, b.ID())
	})
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, newName, inst.Name(), volDBType, b.ID())
		})
	})

	// Rename the volume and its snapshots on the storage device.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	newVolStorageName := project.Instance(inst.Project().Name, newName)
	contentType := InstanceContentType(inst)

	vol := b.GetVolume(volType, contentType, volStorageName, volume.Config)

	err = b.driver.RenameVolume(vol, newVolStorageName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		// Renaming a volume doesn't change its UUID.
		// Pass the same configuration as for the initial rename operation.
		newVol := b.GetVolume(volType, contentType, newVolStorageName, volume.Config)
		_ = b.driver.RenameVolume(newVol, volStorageName, op)
	})

	// Remove old instance symlink and create new one.
	err = b.removeInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), drivers.GetVolumeMountPath(b.name, volType, volStorageName))
	})

	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, newName, drivers.GetVolumeMountPath(b.name, volType, newVolStorageName))
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = b.removeInstanceSymlink(inst.Type(), inst.Project().Name, newName)
	})

	// Remove old instance snapshot symlink and create a new one if needed.
	err = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		err = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, newName)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// DeleteInstance removes the instance's root volume (all snapshots need to be removed first).
func (b *lxdBackend) DeleteInstance(inst instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("DeleteInstance started")
	defer l.Debug("DeleteInstance finished")

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance must not be a snapshot")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	// Get any snapshot volume DB records that the instance has.
	dbVolSnaps, err := VolumeDBSnapshotsGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Check all snapshots are already removed.
	if len(dbVolSnaps) > 0 {
		return fmt.Errorf("Cannot remove an instance volume that has snapshots")
	}

	// Get the volume name on storage.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)

	// Delete the volume from the storage device. Must come after snapshots are removed.
	// Must come before DB VolumeDBDelete so that the volume ID is still available.
	l.Debug("Deleting instance volume", logger.Ctx{"volName": volStorageName})

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		err = b.driver.DeleteVolume(vol, op)
		if err != nil {
			return fmt.Errorf("Error deleting storage volume: %w", err)
		}
	}

	// Remove symlinks.
	err = b.removeInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	err = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	// Remove the volume record from the database.
	err = VolumeDBDelete(b, inst.Project().Name, inst.Name(), vol.Type())
	if err != nil {
		return err
	}

	return nil
}

// UpdateInstance updates an instance volume's config.
func (b *lxdBackend) UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "newDesc": newDesc, "newConfig": newConfig})
	l.Debug("UpdateInstance started")
	defer l.Debug("UpdateInstance finished")

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance cannot be a snapshot")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	volDBType, err := VolumeTypeToDBType(volType)
	if err != nil {
		return err
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	contentType := InstanceContentType(inst)

	// Validate config.
	newVol := b.GetVolume(volType, contentType, volStorageName, newConfig)
	err = b.driver.ValidateVolume(newVol, false)
	if err != nil {
		return err
	}

	// Get current config to compare what has changed.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Apply config changes if there are any.
	changedConfig, userOnly := b.detectChangedConfig(dbVol.Config, newConfig)
	if len(changedConfig) != 0 {
		// Check that the volume's size property isn't being changed.
		if changedConfig["size"] != "" {
			return fmt.Errorf(`Instance volume "size" property cannot be changed`)
		}

		// Check that the volume's size.state property isn't being changed.
		if changedConfig["size.state"] != "" {
			return fmt.Errorf(`Instance volume "size.state" property cannot be changed`)
		}

		// Check that the volume's block.filesystem property isn't being changed.
		if changedConfig["block.filesystem"] != "" {
			return fmt.Errorf(`Instance volume "block.filesystem" property cannot be changed`)
		}

		// Check that the volume's volatile.uuid property isn't being changed.
		if changedConfig["volatile.uuid"] != "" {
			return fmt.Errorf(`Instance volume "volatile.uuid" property cannot be changed`)
		}

		if shared.IsFalseOrEmpty(changedConfig["security.shared"]) && volDBType == cluster.StoragePoolVolumeTypeVM {
			err = allowRemoveSecurityShared(b.state, inst.Project().Name, &dbVol.StorageVolume)
			if err != nil {
				return err
			}
		}

		// Generate the effective root device volume for instance.
		volStorageName := project.Instance(inst.Project().Name, inst.Name())
		curVol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
		err = b.applyInstanceRootDiskOverrides(inst, &curVol)
		if err != nil {
			return err
		}

		if !userOnly {
			err = b.driver.UpdateVolume(curVol, changedConfig)
			if err != nil {
				return err
			}
		}
	}

	// Update the database if something changed.
	if len(changedConfig) != 0 || newDesc != dbVol.Description {
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePoolVolume(ctx, inst.Project().Name, inst.Name(), volDBType, b.ID(), newDesc, newConfig)
		})
		if err != nil {
			return err
		}
	}

	b.state.Events.SendLifecycle(inst.Project().Name, lifecycle.StorageVolumeUpdated.Event(newVol, string(newVol.Type()), inst.Project().Name, op, nil))

	return nil
}

// UpdateInstanceSnapshot updates an instance snapshot volume's description.
// Volume config is not allowed to be updated and will return an error.
func (b *lxdBackend) UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "newDesc": newDesc, "newConfig": newConfig})
	l.Debug("UpdateInstanceSnapshot started")
	defer l.Debug("UpdateInstanceSnapshot finished")

	if !inst.IsSnapshot() {
		return fmt.Errorf("Instance must be a snapshot")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	return b.updateVolumeDescriptionOnly(inst.Project().Name, inst.Name(), volType, newDesc, newConfig, op)
}

// MigrateInstance sends an instance volume for migration.
// The args.Name field is ignored and the name of the instance is used instead.
func (b *lxdBackend) MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "args": fmt.Sprintf("%+v", args)})
	l.Debug("MigrateInstance started")
	defer l.Debug("MigrateInstance finished")

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	if len(args.Snapshots) > 0 && args.FinalSync {
		return fmt.Errorf("Snapshots should not be transferred during final sync")
	}

	if args.Info == nil {
		return fmt.Errorf("Migration info required")
	}

	if args.Info.Config == nil || args.Info.Config.Volume == nil || args.Info.Config.Volume.Config == nil {
		return fmt.Errorf("Volume config is required")
	}

	if len(args.Snapshots) != len(args.Info.Config.VolumeSnapshots) {
		return fmt.Errorf("Requested snapshots count (%d) doesn't match volume snapshot config count (%d)", len(args.Snapshots), len(args.Info.Config.VolumeSnapshots))
	}

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Retrieve a list of snapshots.
	// Afterwards load the volume from the snapshot to ensure the right ordering.
	instSnapshots, err := inst.Snapshots()
	if err != nil {
		return err
	}

	sourceSnapshots := make([]drivers.Volume, 0, len(instSnapshots))
	for _, instSnapshot := range instSnapshots {
		snap, err := VolumeDBGet(b, inst.Project().Name, instSnapshot.Name(), volType)
		if err != nil {
			return err
		}

		snapshotStorageName := project.Instance(inst.Project().Name, snap.Name)
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, snap.Config))
	}

	args.Name = inst.Name() // Override args.Name to ensure instance volume is sent.

	// Send migration index header frame with volume info and wait for receipt if not doing final sync.
	if !args.FinalSync {
		resp, err := b.migrationIndexHeaderSend(l, args.IndexHeaderVersion, conn, args.Info)
		if err != nil {
			return err
		}

		if resp.Refresh != nil {
			args.Refresh = *resp.Refresh
		}
	}

	// Detect if source pool driver doesn't support cheap temporary snapshots that allow consistent copy when
	// running, or if the negotiated protocol is VM non-optimized, meaning a complete raw copy of the active
	// volume is being sent.
	// TODO this can be relaxed in the future if the storage drivers that have RunningCopyFreeze=false make
	// temporary snapshots for block volumes too. But for now this is not the case and we must detect when a
	// generic migration transfer protocol has been negotiated between source and target pools.
	runningCopyFreeze := b.driver.Info().RunningCopyFreeze || args.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC

	// Freeze the instance if not already frozen/stopped, allowInconsistent is not enabled and when its not
	// possible to make a consistent copy with the instance running.
	if !inst.IsSnapshot() && runningCopyFreeze && inst.IsRunning() && !inst.IsFrozen() && !args.AllowInconsistent {
		b.logger.Info("Freezing instance for consistent migration transfer")
		err = inst.Freeze()
		if err != nil {
			return err
		}

		defer func() { _ = inst.Unfreeze() }()

		// Attempt to sync the filesystem.
		_ = filesystem.SyncFS(inst.RootfsPath())
	}

	// Set the parent volume UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, inst.Project().Name)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	err = b.driver.MigrateVolume(volCopy, conn, args, op)
	if err != nil {
		return err
	}

	return nil
}

// CleanupInstancePaths removes any remaining mount paths and symlinks for the instance and its snapshots.
func (b *lxdBackend) CleanupInstancePaths(inst instance.Instance, _ *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("CleanupInstancePaths started")
	defer l.Debug("CleanupInstancePaths finished")

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance must not be a snapshot")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	// Get the volume name on storage.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)

	// Remove empty snapshot mount paths.
	snapshotDir := drivers.GetVolumeSnapshotDir(b.Name(), vol.Type(), vol.Name())

	ents, err := os.ReadDir(snapshotDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed listing instance snapshots directory %q: %w", snapshotDir, err)
	}

	for _, ent := range ents {
		filePath := filepath.Join(snapshotDir, ent.Name())
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			return err
		}

		if !fileInfo.IsDir() {
			continue
		}

		// Remove empty snapshot mount path.
		err = os.Remove(filePath)
		if err != nil {
			return fmt.Errorf("Failed removing instance snapshot mount path %q: %w", filePath, err)
		}
	}

	err = os.Remove(snapshotDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed removing instance snapshots directory %q: %w", snapshotDir, err)
	}

	// Remove empty mount path.
	err = os.Remove(vol.MountPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed removing instance mount path %q: %w", vol.MountPath(), err)
	}

	// Remove symlinks.
	err = b.removeInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return fmt.Errorf("Failed removing instance symlink: %w", err)
	}

	err = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return fmt.Errorf("Failed removing instance snapshots symlink: %w", err)
	}

	return nil
}

// BackupInstance creates an instance backup.
func (b *lxdBackend) BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "optimized": optimized, "snapshots": snapshots})
	l.Debug("BackupInstance started")
	defer l.Debug("BackupInstance finished")

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Ensure the backup file reflects current config.
	err = b.UpdateInstanceBackupFile(inst, snapshots, op)
	if err != nil {
		return err
	}

	var snapNames []string
	var sourceSnapshots []drivers.Volume
	if snapshots {
		// Get snapshots in age order, oldest first, and pass names to storage driver.
		instSnapshots, err := inst.Snapshots()
		if err != nil {
			return err
		}

		snapNames = make([]string, 0, len(instSnapshots))
		sourceSnapshots = make([]drivers.Volume, 0, len(instSnapshots))
		for _, instSnapshot := range instSnapshots {
			// Retrieve the underlying volume.
			snapVol, err := VolumeDBGet(b, inst.Project().Name, instSnapshot.Name(), volType)
			if err != nil {
				return err
			}

			_, snapName, _ := api.GetParentAndSnapshotName(instSnapshot.Name())
			snapNames = append(snapNames, snapName)
			snapshotStorageName := project.Instance(inst.Project().Name, instSnapshot.Name())
			sourceSnapshots = append(sourceSnapshots, b.GetVolume(volType, contentType, snapshotStorageName, snapVol.Config))
		}
	}

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	err = b.driver.BackupVolume(volCopy, tarWriter, optimized, snapNames, op)
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceUsage returns the disk usage of the instance's root volume.
func (b *lxdBackend) GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error) {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("GetInstanceUsage started")
	defer l.Debug("GetInstanceUsage finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, err
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	contentType := InstanceContentType(inst)
	val := VolumeUsage{}

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return nil, err
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)

	// Get the usage
	// If storage driver does not support getting the volume usage, proceed getting the total.
	usedBytes, err := b.driver.GetVolumeUsage(vol)
	if err != nil && !errors.Is(err, drivers.ErrNotSupported) {
		return nil, err
	}

	// If driver does not support getting volume usage, this value should be 0.
	if usedBytes > 0 {
		val.Used = usedBytes
	}

	// Get the total size.
	_, rootDiskConf, err := instancetype.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	sizeStr, ok := rootDiskConf["size"]
	if ok {
		total, err := units.ParseByteSizeString(sizeStr)
		if err != nil {
			return nil, err
		}

		if total >= 0 {
			val.Total = total
		}
	}

	return &val, nil
}

// SetInstanceQuota sets the quota on the instance's root volume.
// Returns ErrInUse if the instance is running and the storage driver doesn't support online resizing.
func (b *lxdBackend) SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "size": size, "vm_state_size": vmStateSize})
	l.Debug("SetInstanceQuota started")
	defer l.Debug("SetInstanceQuota finished")

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentVolume := InstanceContentType(inst)
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Apply the main volume quota.
	vol := b.GetVolume(volType, contentVolume, volStorageName, dbVol.Config)
	err = b.driver.SetVolumeQuota(vol, size, false, op)
	if err != nil {
		return err
	}

	// Apply the filesystem volume quota (only when main volume is block).
	if vol.IsVMBlock() {
		// Apply default VM config filesystem size if main volume size is specified and no custom
		// vmStateSize is specified. This way if the main volume size is empty (i.e removing quota) then
		// this will also pass empty quota for the config filesystem volume as well, allowing a former
		// quota to be removed from both volumes.
		if vmStateSize == "" && size != "" {
			vmStateSize = b.driver.Info().DefaultVMBlockFilesystemSize
		}

		fsVol := vol.NewVMBlockFilesystemVolume()
		err := b.driver.SetVolumeQuota(fsVol, vmStateSize, false, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountInstance mounts the instance's root volume.
func (b *lxdBackend) MountInstance(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("MountInstance started")
	defer l.Debug("MountInstance finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	contentType := InstanceContentType(inst)

	// Get the volume.
	var vol drivers.Volume
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	if inst.ID() > -1 {
		// Load storage volume from database.
		dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
		if err != nil {
			return nil, err
		}

		// Generate the effective root device volume for instance.
		vol = b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
		err = b.applyInstanceRootDiskOverrides(inst, &vol)
		if err != nil {
			return nil, err
		}
	} else {
		contentType := InstanceContentType(inst)
		vol = b.GetVolume(volType, contentType, volStorageName, nil)
	}

	err = b.driver.MountVolume(vol, op)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _, _ = b.driver.UnmountVolume(vol, false, op) })

	diskPath, err := b.getInstanceDisk(inst)
	if err != nil && !errors.Is(err, drivers.ErrNotSupported) {
		return nil, fmt.Errorf("Failed getting disk path: %w", err)
	}

	var mountInfo MountInfo

	if diskPath != "" {
		mountInfo.DevSource = config.DevSourcePath{
			Path: diskPath,
		}
	}

	revert.Success() // From here on it is up to caller to call UnmountInstance() when done.

	// Handle delegation.
	if b.driver.CanDelegateVolume(vol) {
		mountInfo.PostHooks = append(mountInfo.PostHooks, func(inst instance.Instance) error {
			pid := inst.InitPID()

			// Only apply to running instances.
			if pid < 1 {
				return nil
			}

			return b.driver.DelegateVolume(vol, pid)
		})
	}

	return &mountInfo, nil
}

// UnmountInstance unmounts the instance's root volume.
func (b *lxdBackend) UnmountInstance(inst instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("UnmountInstance started")
	defer l.Debug("UnmountInstance finished")

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Get the volume.
	var vol drivers.Volume
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	if inst.ID() > -1 {
		// Load storage volume from database.
		dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
		if err != nil {
			return err
		}

		// Generate the effective root device volume for instance.
		vol = b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
		err = b.applyInstanceRootDiskOverrides(inst, &vol)
		if err != nil {
			return err
		}
	} else {
		vol = b.GetVolume(volType, contentType, volStorageName, nil)
	}

	_, err = b.driver.UnmountVolume(vol, false, op)

	return err
}

// getInstanceDisk returns the location of the disk.
func (b *lxdBackend) getInstanceDisk(inst instance.Instance) (string, error) {
	if inst.Type() != instancetype.VM {
		return "", drivers.ErrNotSupported
	}

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return "", err
	}

	contentType := InstanceContentType(inst)
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return "", err
	}

	// Get the volume.
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)

	// Get the location of the disk block device.
	diskPath, err := b.driver.GetVolumeDiskPath(vol)
	if err != nil {
		return "", err
	}

	return diskPath, nil
}

// CreateInstanceSnapshot creates a snaphot of an instance volume.
func (b *lxdBackend) CreateInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "src": src.Name()})
	l.Debug("CreateInstanceSnapshot started")
	defer l.Debug("CreateInstanceSnapshot finished")

	if inst.Type() != src.Type() {
		return fmt.Errorf("Instance types must match")
	}

	if !inst.IsSnapshot() {
		return fmt.Errorf("Instance must be a snapshot")
	}

	if src.IsSnapshot() {
		return fmt.Errorf("Source instance cannot be a snapshot")
	}

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	srcDBVol, err := VolumeDBGet(b, src.Project().Name, src.Name(), volType)
	if err != nil {
		return err
	}

	parentUUID := srcDBVol.Config["volatile.uuid"]
	if parentUUID == "" {
		return fmt.Errorf(`Instance volume %q is missing the required "volatile.uuid" setting`, src.Name())
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Get the volume.
	vol := b.GetNewVolume(volType, contentType, volStorageName, srcDBVol.Config)

	// Set the parent volume's UUID.
	vol.SetParentUUID(parentUUID)

	revert := revert.New()
	defer revert.Fail()

	// Lock this operation to ensure that the only one snapshot is made at the time.
	// Other operations will wait for this one to finish.
	unlock, err := locking.Lock(context.TODO(), drivers.OperationLockName("CreateInstanceSnapshot", b.name, vol.Type(), contentType, src.Name()))
	if err != nil {
		return err
	}

	defer unlock()

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), srcDBVol.Description, volType, true, vol.Config(), inst.CreationDate(), time.Time{}, contentType, false, true)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

	// Some driver backing stores require that running instances be frozen during snapshot.
	if b.driver.Info().RunningCopyFreeze && src.IsRunning() && !src.IsFrozen() {
		// Freeze the processes.
		err = src.Freeze()
		if err != nil {
			return err
		}

		defer func() { _ = src.Unfreeze() }()

		// Attempt to sync the filesystem.
		_ = filesystem.SyncFS(src.RootfsPath())
	}

	err = b.driver.CreateVolumeSnapshot(vol, op)
	if err != nil {
		return err
	}

	err = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// RenameInstanceSnapshot renames an instance snapshot.
func (b *lxdBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "newName": newName})
	l.Debug("RenameInstanceSnapshot started")
	defer l.Debug("RenameInstanceSnapshot finished")

	revert := revert.New()
	defer revert.Fail()

	if !inst.IsSnapshot() {
		return fmt.Errorf("Instance must be a snapshot")
	}

	if shared.IsSnapshot(newName) {
		return fmt.Errorf("New name cannot be a snapshot")
	}

	// Quick checks.
	err := instancetype.ValidName(newName, false)
	if err != nil {
		return err
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	volDBType, err := VolumeTypeToDBType(volType)
	if err != nil {
		return err
	}

	parentName, oldSnapshotName, isSnap := api.GetParentAndSnapshotName(inst.Name())
	if !isSnap {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	contentType := InstanceContentType(inst)
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Rename storage volume snapshot.
	snapVol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.driver.RenameVolumeSnapshot(snapVol, newName, op)
	if err != nil {
		return err
	}

	newVolName := drivers.GetSnapshotVolumeName(parentName, newName)

	revert.Add(func() {
		// Revert rename.
		// Renaming a volume snapshot doesn't change its UUID.
		// Pass the same configuration as for the initial rename operation.
		newSnapVol := b.GetVolume(volType, contentType, project.Instance(inst.Project().Name, newVolName), dbVol.Config)
		_ = b.driver.RenameVolumeSnapshot(newSnapVol, oldSnapshotName, op)
	})

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Rename DB volume record.
		return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, inst.Name(), newVolName, volDBType, b.ID())
	})
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Rename DB volume record back.
			return tx.RenameStoragePoolVolume(ctx, inst.Project().Name, newVolName, inst.Name(), volDBType, b.ID())
		})
	})

	// Ensure the backup file reflects current config.
	err = b.UpdateInstanceBackupFile(inst, true, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// DeleteInstanceSnapshot removes the snapshot volume for the supplied snapshot instance.
func (b *lxdBackend) DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("DeleteInstanceSnapshot started")
	defer l.Debug("DeleteInstanceSnapshot finished")

	parentName, snapName, isSnap := api.GetParentAndSnapshotName(inst.Name())
	if !inst.IsSnapshot() || !isSnap {
		return fmt.Errorf("Instance must be a snapshot")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Get the parent volume name on storage.
	parentStorageName := project.Instance(inst.Project().Name, parentName)

	// Delete the snapshot from the storage device.
	// Must come before DB VolumeDBDelete so that the volume ID is still available.
	l.Debug("Deleting instance snapshot volume", logger.Ctx{"volName": parentStorageName, "snapshotName": snapName})

	snapVolName := drivers.GetSnapshotVolumeName(parentStorageName, snapName)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	vol := b.GetVolume(volType, contentType, snapVolName, dbVol.Config)

	// Set the parent volume UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, inst.Project().Name)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		err = b.driver.DeleteVolumeSnapshot(vol, op)
		if err != nil {
			return err
		}
	}

	// Delete symlink if needed.
	err = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, inst.Name())
	if err != nil {
		return err
	}

	// Remove the snapshot volume record from the database if exists.
	err = VolumeDBDelete(b, inst.Project().Name, inst.Name(), vol.Type())
	if err != nil {
		return err
	}

	return nil
}

// RestoreInstanceSnapshot restores an instance snapshot.
func (b *lxdBackend) RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "src": src.Name()})
	l.Debug("RestoreInstanceSnapshot started")
	defer l.Debug("RestoreInstanceSnapshot finished")

	revert := revert.New()
	defer revert.Fail()

	if inst.Type() != src.Type() {
		return fmt.Errorf("Instance types must match")
	}

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance must not be snapshot")
	}

	if !src.IsSnapshot() {
		return fmt.Errorf("Source instance must be a snapshot")
	}

	// Target instance must not be running.
	if inst.IsRunning() {
		return fmt.Errorf("Instance must not be running to restore")
	}

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	if !shared.IsSnapshot(src.Name()) {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	// Load storage volume from database.
	srcDBVol, err := VolumeDBGet(b, src.Project().Name, src.Name(), volType)
	if err != nil {
		return err
	}

	// Restore snapshot volume config if different.
	changedConfig, _ := b.detectChangedConfig(dbVol.Config, srcDBVol.Config)
	if len(changedConfig) != 0 || dbVol.Description != srcDBVol.Description {
		volDBType, err := VolumeTypeToDBType(volType)
		if err != nil {
			return err
		}

		volUUID := dbVol.Config["volatile.uuid"]
		if volUUID == "" {
			return fmt.Errorf(`Instance volume %q is missing the required "volatile.uuid" setting`, inst.Name())
		}

		// Set the actual target volume's UUID.
		// This is required because we have just copied the source volume's config.
		srcDBVol.Config["volatile.uuid"] = volUUID

		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePoolVolume(ctx, inst.Project().Name, inst.Name(), volDBType, b.ID(), srcDBVol.Description, srcDBVol.Config)
		})
		if err != nil {
			return err
		}

		revert.Add(func() {
			// Update the instance snapshot with its old config.
			// This will also reset its UUID.
			_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpdateStoragePoolVolume(ctx, inst.Project().Name, inst.Name(), volDBType, b.ID(), dbVol.Description, dbVol.Config)
			})
		})
	}

	// Get the volume's snapshot from the DB.
	dbSnapVol, err := VolumeDBGet(b, src.Project().Name, src.Name(), volType)
	if err != nil {
		return err
	}

	snapshotStorageName := project.StorageVolume(src.Project().Name, dbSnapVol.Name)
	snapVol := b.GetVolume(volType, contentType, snapshotStorageName, dbSnapVol.Config)

	err = b.driver.RestoreVolume(vol, snapVol, op)
	if err != nil {
		snapErr, ok := err.(drivers.ErrDeleteSnapshots)
		if ok {
			// We need to delete some snapshots and try again.
			snaps, err := inst.Snapshots()
			if err != nil {
				return err
			}

			// Go through all the snapshots.
			for _, snap := range snaps {
				_, snapName, _ := api.GetParentAndSnapshotName(snap.Name())
				if !shared.ValueInSlice(snapName, snapErr.Snapshots) {
					continue
				}

				// Delete snapshot instance if listed in the error as one that needs removing.
				err := snap.Delete(true)
				if err != nil {
					return err
				}
			}

			// Now try restoring again.
			err = b.driver.RestoreVolume(vol, snapVol, op)
			if err != nil {
				return err
			}

			return nil
		}

		return err
	}

	revert.Success()
	return nil
}

// MountInstanceSnapshot mounts an instance snapshot. It is mounted as read only so that the
// snapshot cannot be modified.
func (b *lxdBackend) MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("MountInstanceSnapshot started")
	defer l.Debug("MountInstanceSnapshot finished")

	if !inst.IsSnapshot() {
		return nil, fmt.Errorf("Instance must be a snapshot")
	}

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return nil, err
	}

	contentType := InstanceContentType(inst)

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return nil, err
	}

	// Set the parent volume UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, inst.Project().Name)
		if err != nil {
			return nil, err
		}

		vol.SetParentUUID(parentUUID)
	}

	// Mount the snapshot.
	err = b.driver.MountVolumeSnapshot(vol, op)
	if err != nil {
		return nil, err
	}

	diskPath, err := b.getInstanceDisk(inst)
	if err != nil && !errors.Is(err, drivers.ErrNotSupported) {
		return nil, fmt.Errorf("Failed getting disk path: %w", err)
	}

	var mountInfo MountInfo

	if diskPath != "" {
		mountInfo.DevSource = config.DevSourcePath{
			Path: diskPath,
		}
	}

	return &mountInfo, nil
}

// UnmountInstanceSnapshot unmounts an instance snapshot.
func (b *lxdBackend) UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("UnmountInstanceSnapshot started")
	defer l.Debug("UnmountInstanceSnapshot finished")

	if !inst.IsSnapshot() {
		return fmt.Errorf("Instance must be a snapshot")
	}

	// Check we can convert the instance to the volume type needed.
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)

	// Load storage volume from database.
	dbVol, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return err
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := b.GetVolume(volType, contentType, volStorageName, dbVol.Config)
	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return err
	}

	// Set the parent volume UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, inst.Project().Name)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	// Unmount volume.
	_, err = b.driver.UnmountVolumeSnapshot(vol, op)

	return err
}

// EnsureImage creates an optimized volume of the image if supported by the storage pool driver and the volume
// doesn't already exist. If the volume already exists then it is checked to ensure it matches the pools current
// volume settings ("volume.size" and "block.filesystem" if applicable). If not the optimized volume is removed
// and regenerated to apply the pool's current volume settings.
func (b *lxdBackend) EnsureImage(fingerprint string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"fingerprint": fingerprint})
	l.Debug("EnsureImage started")
	defer l.Debug("EnsureImage finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.driver.Info().OptimizedImages {
		return nil // Nothing to do for drivers that don't support optimized images volumes.
	}

	// We need to lock this operation to ensure that the image is not being created multiple times.
	// Uses a lock name of "EnsureImage_<fingerprint>" to avoid deadlocking with CreateVolume below that also
	// establishes a lock on the volume type & name if it needs to mount the volume before filling.
	unlock, err := locking.Lock(context.TODO(), drivers.OperationLockName("EnsureImage", b.name, drivers.VolumeTypeImage, "", fingerprint))
	if err != nil {
		return err
	}

	defer unlock()

	var image *api.Image

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load image info from database.
		_, image, err = tx.GetImageFromAnyProject(ctx, fingerprint)

		return err
	})
	if err != nil {
		return err
	}

	// Derive content type from image type. Image types are not the same as instance types, so don't use
	// instance type constants for comparison.
	contentType := drivers.ContentTypeFS

	if image.Type == "virtual-machine" {
		contentType = drivers.ContentTypeBlock
	}

	// Try and load any existing volume config on this storage pool so we can compare filesystems if needed.
	imgDBVol, err := VolumeDBGet(b, api.ProjectDefaultName, image.Fingerprint, drivers.VolumeTypeImage)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	// Create the new image volume. No config for an image volume so set to nil.
	// Pool config values will be read by the underlying driver if needed.
	imgVol := b.GetVolume(drivers.VolumeTypeImage, contentType, image.Fingerprint, nil)

	// If an existing DB row was found, check if filesystem is the same as the current pool's filesystem.
	// If not we need to delete the existing cached image volume and re-create using new filesystem.
	// We need to do this for VM block images too, as they create a filesystem based config volume too.
	if imgDBVol != nil {
		// Generate a temporary volume instance that represents how a new volume using pool defaults would
		// be configured.
		tmpImgVol := imgVol.Clone()
		err := b.Driver().FillVolumeConfig(tmpImgVol)
		if err != nil {
			return err
		}

		// Add existing image volume's config to imgVol.
		imgVol = b.GetVolume(drivers.VolumeTypeImage, contentType, image.Fingerprint, imgDBVol.Config)

		// Check if the volume's block backed mode differs from the pool's current setting for new volumes.
		blockModeChanged := tmpImgVol.IsBlockBacked() != imgVol.IsBlockBacked()

		// Check if the volume is block backed and its filesystem is different from the pool's current
		// setting for new volumes.
		blockFSChanged := imgVol.IsBlockBacked() && imgVol.Config()["block.filesystem"] != tmpImgVol.Config()["block.filesystem"]

		// If the existing image volume no longer matches the pool's settings for new volumes then we need
		// to delete and re-create it.
		if blockModeChanged || blockFSChanged {
			if blockModeChanged {
				l.Debug("Block mode has changed, regenerating image volume")
			} else {
				l.Debug("Block volume filesystem of pool has changed since cached image volume created, regenerating image volume")
			}

			err = b.DeleteImage(image.Fingerprint, op)
			if err != nil {
				return err
			}

			// Reset img volume as we just deleted the old one.
			imgDBVol = nil
		}
	}

	if imgDBVol == nil {
		// Instantiate a new volume including its own UUID.
		imgVol = b.GetNewVolume(drivers.VolumeTypeImage, contentType, image.Fingerprint, nil)
	}

	// Check if we already have a suitable volume on storage device.
	volExists, err := b.driver.HasVolume(imgVol)
	if err != nil {
		return err
	}

	if volExists {
		if imgDBVol != nil {
			// Work out what size the image volume should be as if we were creating from scratch.
			// This takes into account the existing volume's "volatile.rootfs.size" setting if set so
			// as to avoid trying to shrink a larger image volume back to the default size when it is
			// allowed to be larger than the default as the pool doesn't specify a volume.size.
			l.Debug("Checking image volume size")
			newVolSize, err := imgVol.ConfigSizeFromSource(imgVol)
			if err != nil {
				return err
			}

			imgVol.SetConfigSize(newVolSize)

			// Try applying the current size policy to the existing volume. If it is the same the
			// driver should make no changes, and if not then attempt to resize it to the new policy.
			l.Debug("Setting image volume size", logger.Ctx{"size": imgVol.ConfigSize()})
			err = b.driver.SetVolumeQuota(imgVol, imgVol.ConfigSize(), false, op)
			if errors.Is(err, drivers.ErrCannotBeShrunk) || errors.Is(err, drivers.ErrNotSupported) {
				// If the driver cannot resize the existing image volume to the new policy size
				// then delete the image volume and try to recreate using the new policy settings.
				l.Debug("Volume size of pool has changed since cached image volume created and cached volume cannot be resized, regenerating image volume")
				err = b.DeleteImage(image.Fingerprint, op)
				if err != nil {
					return err
				}

				// Reset img volume variables as we just deleted the old one.
				// Since the old volume has been removed, ensure the new volume
				// is instantiated with its own UUID.
				imgDBVol = nil
				imgVol = b.GetNewVolume(drivers.VolumeTypeImage, contentType, image.Fingerprint, nil)
			} else if err != nil {
				return err
			} else {
				// We already have a valid volume at the correct size, just return.
				return nil
			}
		} else {
			// We have an unrecorded on-disk volume, assume it's a partial unpack and delete it.
			// This can occur if LXD process exits unexpectedly during an image unpack or if the
			// storage pool has been recovered (which would not recreate the image volume DB records).
			l.Warn("Deleting leftover/partially unpacked image volume")
			err = b.driver.DeleteVolume(imgVol, op)
			if err != nil {
				return fmt.Errorf("Failed deleting leftover/partially unpacked image volume: %w", err)
			}
		}
	}

	volFiller := drivers.VolumeFiller{
		Fingerprint: image.Fingerprint,
		Fill:        b.imageFiller(image.Fingerprint, op),
	}

	revert := revert.New()
	defer revert.Fail()

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, api.ProjectDefaultName, image.Fingerprint, "", drivers.VolumeTypeImage, false, imgVol.Config(), time.Now().UTC(), time.Time{}, contentType, false, false)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, api.ProjectDefaultName, image.Fingerprint, drivers.VolumeTypeImage) })

	err = b.driver.CreateVolume(imgVol, &volFiller, op)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = b.driver.DeleteVolume(imgVol, op) })

	// If the volume filler has recorded the size of the unpacked volume, then store this in the image DB row.
	if volFiller.Size != 0 {
		imgVol.Config()["volatile.rootfs.size"] = strconv.FormatInt(volFiller.Size, 10)

		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePoolVolume(ctx, api.ProjectDefaultName, image.Fingerprint, cluster.StoragePoolVolumeTypeImage, b.id, "", imgVol.Config())
		})
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// shouldUseOptimizedImage determines if an optimized image should be used based on the provided volume config.
// It returns true if the volume config aligns with the pool's default configuration, and an optimized image does
// not exist or also matches the pool's default configuration.
func (b *lxdBackend) shouldUseOptimizedImage(fingerprint string, contentType drivers.ContentType, volConfig map[string]string) (bool, error) {
	canOptimizeImage := b.driver.Info().OptimizedImages

	// If the volume config is empty, the default pool configuration is used, making the driver's support
	// for optimized images the determining factor. However, an optimized image cannot be utilized if the
	// driver lacks support for it.
	if !canOptimizeImage || len(volConfig) == 0 {
		return canOptimizeImage, nil
	}

	// Create the image volume with the provided volume config.
	newImgVol := b.GetVolume(drivers.VolumeTypeImage, contentType, fingerprint, volConfig)
	err := b.Driver().FillVolumeConfig(newImgVol)
	if err != nil {
		return false, err
	}

	// Create the image volume with pool's default settings.
	poolDefaultImgVol := b.GetVolume(drivers.VolumeTypeImage, contentType, fingerprint, nil)
	err = b.Driver().FillVolumeConfig(poolDefaultImgVol)
	if err != nil {
		return false, err
	}

	// If the new volume's config doesn't match the pool's default configuration, don't use an optimized image.
	if !volumeConfigsMatch(newImgVol, poolDefaultImgVol) {
		return false, nil
	}

	// Load existing optimized image, if it exists.
	imgDBVol, err := VolumeDBGet(b, api.ProjectDefaultName, fingerprint, drivers.VolumeTypeImage)
	if err != nil && !response.IsNotFoundError(err) {
		return false, err
	}

	if imgDBVol != nil {
		// Ensure existing optimized image's config matches the pool's default configuration.
		imgVol := b.GetVolume(drivers.VolumeTypeImage, contentType, fingerprint, imgDBVol.Config)
		if !volumeConfigsMatch(newImgVol, imgVol) {
			return false, nil
		}
	}

	return true, nil
}

// volumeConfigsMatch checks if the block-backed modes of two volumes match, and if they are block-backed, ensures
// their filesystem configurations are also identical.
func volumeConfigsMatch(vol1, vol2 drivers.Volume) bool {
	blockModeChanged := vol1.IsBlockBacked() != vol2.IsBlockBacked()
	blockFSChanged := vol1.IsBlockBacked() && vol1.Config()["block.filesystem"] != vol2.Config()["block.filesystem"]

	// TODO: Temporary workaround for zfs.blocksize issue:
	// When zfs.blocksize changes, a new optimized image isn't generated. This ensures we don't use an
	// optimized image if initial.zfs.blocksize differs from the default pool settings.
	//
	// Note: If initial.zfs.blocksize is set to 8KiB and volume.zfs.blocksize is unset (defaults to 8KiB),
	// they're considered unequal ("" != "8KiB"), preventing the use of a matching optimized image.
	blockSizeChanged := vol1.IsBlockBacked() && vol1.Config()["zfs.blocksize"] != vol2.Config()["zfs.blocksize"]

	return !blockModeChanged && !blockFSChanged && !blockSizeChanged
}

// DeleteImage removes an image from the database and underlying storage device if needed.
func (b *lxdBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"fingerprint": fingerprint})
	l.Debug("DeleteImage started")
	defer l.Debug("DeleteImage finished")

	// We need to lock this operation to ensure that the image is not being deleted multiple times.
	unlock, err := locking.Lock(context.TODO(), drivers.OperationLockName("DeleteImage", b.name, drivers.VolumeTypeImage, "", fingerprint))
	if err != nil {
		return err
	}

	defer unlock()

	// Load the storage volume in order to get the volume config which is needed for some drivers.
	imgDBVol, err := VolumeDBGet(b, api.ProjectDefaultName, fingerprint, drivers.VolumeTypeImage)
	if err != nil {
		return err
	}

	// Get the content type.
	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(imgDBVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	vol := b.GetVolume(drivers.VolumeTypeImage, contentType, fingerprint, imgDBVol.Config)

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		err = b.driver.DeleteVolume(vol, op)
		if err != nil {
			return err
		}
	}

	err = VolumeDBDelete(b, api.ProjectDefaultName, fingerprint, vol.Type())
	if err != nil {
		return err
	}

	b.state.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.StorageVolumeDeleted.Event(vol, string(vol.Type()), api.ProjectDefaultName, op, nil))

	return nil
}

// updateVolumeDescriptionOnly is a helper function used when handling update requests for volumes
// that only allow their descriptions to be updated. If any config supplied differs from the
// current volume's config then an error is returned.
func (b *lxdBackend) updateVolumeDescriptionOnly(projectName string, volName string, volType drivers.VolumeType, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	volDBType, err := VolumeTypeToDBType(volType)
	if err != nil {
		return err
	}

	// Get current config to compare what has changed.
	curVol, err := VolumeDBGet(b, projectName, volName, volType)
	if err != nil {
		return err
	}

	if newConfig != nil {
		changedConfig, _ := b.detectChangedConfig(curVol.Config, newConfig)
		if len(changedConfig) != 0 {
			return fmt.Errorf("Volume config is not editable")
		}
	}

	// Update the database if description changed. Use current config.
	if newDesc != curVol.Description {
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePoolVolume(ctx, projectName, volName, volDBType, b.ID(), newDesc, curVol.Config)
		})
		if err != nil {
			return err
		}
	}

	// Get content type.
	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(curVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	// Validate config.
	vol := b.GetVolume(drivers.VolumeType(curVol.Type), contentType, volName, newConfig)

	if !vol.IsSnapshot() {
		b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeUpdated.Event(vol, string(vol.Type()), projectName, op, nil))
	} else {
		b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeSnapshotUpdated.Event(vol, string(vol.Type()), projectName, op, nil))
	}

	return nil
}

// UpdateImage updates image config.
func (b *lxdBackend) UpdateImage(fingerprint, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"fingerprint": fingerprint, "newDesc": newDesc, "newConfig": newConfig})
	l.Debug("UpdateImage started")
	defer l.Debug("UpdateImage finished")

	return b.updateVolumeDescriptionOnly(api.ProjectDefaultName, fingerprint, drivers.VolumeTypeImage, newDesc, newConfig, op)
}

// CreateBucket creates an object bucket.
func (b *lxdBackend) CreateBucket(projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucket.Name, "desc": bucket.Description, "config": bucket.Config})
	l.Debug("CreateBucket started")
	defer l.Debug("CreateBucket finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.Driver().Info().Buckets {
		return fmt.Errorf("Storage pool does not support buckets")
	}

	// Must be defined before revert so that its not cancelled by time revert.Fail runs.
	ctx, ctxCancel := context.WithTimeout(context.TODO(), time.Duration(time.Second*30))
	defer ctxCancel()

	// Validate config and create database entry for new storage bucket.
	revert := revert.New()
	defer revert.Fail()

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := b.GetNewVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

	// Set the new bucket volume's UUID.
	bucket.Config["volatile.uuid"] = bucketVol.Config()["volatile.uuid"]

	bucketID, err := BucketDBCreate(context.TODO(), b, projectName, memberSpecific, &bucket)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = BucketDBDelete(context.TODO(), b, bucketID) })

	// Create the bucket on the storage device.
	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.
		err := b.driver.CreateVolume(bucketVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = b.driver.DeleteVolume(bucketVol, op) })

		// Start minio process.
		minioProc, err := b.ActivateBucket(projectName, bucket.Name, op)
		if err != nil {
			return err
		}

		s3Client, err := minioProc.S3Client()
		if err != nil {
			return err
		}

		bucketExists, err := s3Client.BucketExists(ctx, bucket.Name)
		if err != nil {
			return fmt.Errorf("Failed checking if bucket exists: %w", err)
		}

		if bucketExists {
			return api.StatusErrorf(http.StatusConflict, "A bucket for that name already exists")
		}

		// Create new bucket.
		err = s3Client.MakeBucket(ctx, bucket.Name, minio.MakeBucketOptions{})
		if err != nil {
			return fmt.Errorf("Failed creating bucket: %w", err)
		}

		revert.Add(func() { _ = s3Client.RemoveBucket(ctx, bucket.Name) })
	} else {
		// Handle per-driver implementation for remote storage drivers.
		err = b.driver.CreateBucket(bucketVol, op)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// UpdateBucket updates an object bucket.
func (b *lxdBackend) UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut, _ *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucketName, "desc": bucket.Description, "config": bucket.Config})
	l.Debug("UpdateBucket started")
	defer l.Debug("UpdateBucket finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.Driver().Info().Buckets {
		return fmt.Errorf("Storage pool does not support buckets")
	}

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	// Get current config to compare what has changed.
	var curBucket *db.StorageBucket
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		curBucket, err = tx.GetStoragePoolBucket(ctx, b.id, projectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return err
	}

	bucketVolName := project.StorageVolume(projectName, curBucket.Name)

	curBucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, curBucket.Config)

	// Validate config.
	newBucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

	err = b.driver.ValidateBucket(newBucketVol)
	if err != nil {
		return err
	}

	err = b.driver.ValidateVolume(newBucketVol, false)
	if err != nil {
		return err
	}

	curBucketEtagHash, err := util.EtagHash(curBucket.Etag())
	if err != nil {
		return err
	}

	newBucket := api.StorageBucket{
		Name:        curBucket.Name,
		Description: bucket.Description,
		Config:      bucket.Config,
	}

	newBucketEtagHash, err := util.EtagHash(newBucket.Etag())
	if err != nil {
		return err
	}

	if curBucketEtagHash == newBucketEtagHash {
		return nil // Nothing has changed.
	}

	changedConfig, userOnly := b.detectChangedConfig(curBucket.Config, bucket.Config)
	if len(changedConfig) > 0 && !userOnly {
		if memberSpecific {
			// Stop MinIO process if running so volume can be resized if needed.
			minioProc, err := miniod.Get(curBucketVol.Name())
			if err != nil {
				return err
			}

			if minioProc != nil {
				err = minioProc.Stop(context.Background())
				if err != nil {
					return fmt.Errorf("Failed stopping bucket: %w", err)
				}
			}

			err = b.driver.UpdateVolume(curBucketVol, changedConfig)
			if err != nil {
				return err
			}
		} else {
			// Handle per-driver implementation for remote storage drivers.
			err = b.driver.UpdateBucket(curBucketVol, changedConfig)
			if err != nil {
				return err
			}
		}
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Update the database record.
		return tx.UpdateStoragePoolBucket(ctx, b.id, curBucket.ID, bucket)
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteBucket deletes an object bucket.
func (b *lxdBackend) DeleteBucket(projectName string, bucketName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucketName})
	l.Debug("DeleteBucket started")
	defer l.Debug("DeleteBucket finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.Driver().Info().Buckets {
		return fmt.Errorf("Storage pool does not support buckets")
	}

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	var bucket *db.StorageBucket
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, b.id, projectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return err
	}

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.

		// Stop MinIO process if running.
		minioProc, err := miniod.Get(bucketVolName)
		if err != nil {
			return err
		}

		if minioProc != nil {
			err = minioProc.Stop(context.Background())
			if err != nil {
				return fmt.Errorf("Failed stopping bucket: %w", err)
			}
		}

		vol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, nil)
		err = b.driver.DeleteVolume(vol, op)
		if err != nil {
			return err
		}
	} else {
		// Handle per-driver implementation for remote storage drivers.
		err = b.driver.DeleteBucket(bucketVol, op)
		if err != nil {
			return err
		}
	}

	_ = BucketDBDelete(context.TODO(), b, bucket.ID)
	if err != nil {
		return err
	}

	return nil
}

// ImportBucket takes an existing bucket on the storage backend and ensures that the DB records
// are restored as needed to make it operational with LXD.
// Used during the recovery import stage.
func (b *lxdBackend) ImportBucket(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	if poolVol.Bucket == nil {
		return nil, fmt.Errorf("Invalid pool bucket config supplied")
	}

	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": poolVol.Bucket.Name})
	l.Debug("ImportBucket started")
	defer l.Debug("ImportBucket finished")

	revert := revert.New()
	defer revert.Fail()

	// Copy bucket config from backup file if present (so BucketDBCreate can safely modify the copy if needed).
	bucketConfig := make(map[string]string, len(poolVol.Bucket.Config))
	for k, v := range poolVol.Bucket.Config {
		bucketConfig[k] = v
	}

	bucket := &api.StorageBucketsPost{
		Name:             poolVol.Bucket.Name,
		StorageBucketPut: poolVol.Bucket.Writable(),
	}

	// Get the bucket name on storage.
	storageBucketName := project.StorageVolume(projectName, bucket.Name)
	storageBucket := b.GetNewVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, storageBucketName, bucketConfig)

	// Set the bucket volume's UUID.
	bucket.Config["volatile.uuid"] = storageBucket.Config()["volatile.uuid"]

	// Validate config and create database entry for restored bucket.
	bucketID, err := BucketDBCreate(b.state.ShutdownCtx, b, projectName, true, bucket)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = BucketDBDelete(b.state.ShutdownCtx, b, bucketID) })

	err = b.driver.ValidateVolume(storageBucket, false)
	if err != nil {
		return nil, err
	}

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	if !memberSpecific {
		return nil, fmt.Errorf("Importing buckets from a remote storage is not supported")
	}

	// Handle common MinIO implementation for local storage drivers.

	// Extract existing bucket keys from MinIO.
	keys, err := b.recoverMinIOKeys(projectName, bucket.Name, op)
	if err != nil {
		return nil, err
	}

	// Insert keys into the database.
	for _, key := range keys {
		var keyID int64

		err := b.state.DB.Cluster.Transaction(b.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			keyID, err = tx.CreateStoragePoolBucketKey(ctx, bucketID, key)

			return err
		})
		if err != nil {
			return nil, err
		}

		revert.Add(func() {
			_ = b.state.DB.Cluster.Transaction(b.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.DeleteStoragePoolBucketKey(ctx, bucketID, keyID)
			})
		})
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// recoverMinIOKeys retrieves existing bucket keys from MinIO for each service account associated with the given bucket.
func (b *lxdBackend) recoverMinIOKeys(projectName string, bucketName string, op *operations.Operation) ([]api.StorageBucketKeysPost, error) {
	// Start minio process.
	minioProc, err := b.ActivateBucket(projectName, bucketName, op)
	if err != nil {
		return nil, err
	}

	// Initialize minio client object.
	adminClient, err := minioProc.AdminClient()
	if err != nil {
		return nil, err
	}

	ctx, ctxCancel := context.WithTimeout(b.state.ShutdownCtx, time.Duration(time.Second*30))
	defer ctxCancel()

	// Export IAM data (response is ZIP file).
	iamZipReader, err := adminClient.ExportIAM(ctx)
	if err != nil {
		return nil, err
	}

	unmarshal := func(file *zip.File, into any) error {
		f, err := file.Open()
		if err != nil {
			return err
		}

		defer f.Close()

		fContent, err := io.ReadAll(f)
		if err != nil {
			return err
		}

		return json.Unmarshal(fContent, into)
	}

	// We are interesed only in a json file that contains service accounts.
	// Find that file and extract service accounts.
	svcAccounts := map[string]miniod.Credentials{}
	for _, file := range iamZipReader.File {
		if file.Name != "iam-assets/svcaccts.json" {
			continue
		}

		err := unmarshal(file, &svcAccounts)
		if err != nil {
			return nil, err
		}

		break
	}

	recoveredKeys := make([]api.StorageBucketKeysPost, 0, len(svcAccounts))

	// Extract bucket keys for each service account.
	for _, creds := range svcAccounts {
		svcAccountInfo, err := adminClient.InfoServiceAccount(ctx, creds.AccessKey)
		if err != nil {
			return nil, err
		}

		bucketRole, err := s3.BucketPolicyRole(bucketName, svcAccountInfo.Policy)
		if err != nil {
			return nil, err
		}

		key := api.StorageBucketKeysPost{
			Name: creds.AccessKey,
			StorageBucketKeyPut: api.StorageBucketKeyPut{
				Description: "Recovered bucket key",
				Role:        bucketRole,
				AccessKey:   creds.AccessKey,
				SecretKey:   creds.SecretKey,
			},
		}

		recoveredKeys = append(recoveredKeys, key)
	}

	return recoveredKeys, nil
}

// CreateBucketKey creates an object bucket key.
func (b *lxdBackend) CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error) {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucketName, "keyName": key.Name, "desc": key.Description, "role": key.Role})
	l.Debug("CreateBucketKey started")
	defer l.Debug("CreateBucketKey finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, err
	}

	if !b.Driver().Info().Buckets {
		return nil, fmt.Errorf("Storage pool does not support buckets")
	}

	// Must be defined before revert so that its not cancelled by time revert.Fail runs.
	ctx, ctxCancel := context.WithTimeout(context.TODO(), time.Duration(time.Second*30))
	defer ctxCancel()

	revert := revert.New()
	defer revert.Fail()

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	var bucket *db.StorageBucket
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, b.id, projectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return nil, err
	}

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

	// Create the bucket key on the storage device.
	creds := drivers.S3Credentials{
		AccessKey: key.AccessKey,
		SecretKey: key.SecretKey,
	}

	err = b.driver.ValidateBucketKey(key.Name, creds, key.Role)
	if err != nil {
		return nil, err
	}

	var newCreds *drivers.S3Credentials

	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.

		// Start minio process.
		minioProc, err := b.ActivateBucket(projectName, bucket.Name, op)
		if err != nil {
			return nil, err
		}

		bucketPolicy, err := s3.BucketPolicy(bucket.Name, key.Role)
		if err != nil {
			return nil, err
		}

		adminClient, err := minioProc.AdminClient()
		if err != nil {
			return nil, err
		}

		adminCreds, err := adminClient.AddServiceAccount(ctx, miniod.ServiceAccountArgs{
			Policy:    bucketPolicy,
			AccessKey: key.AccessKey,
			SecretKey: key.SecretKey,
		})
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = adminClient.DeleteServiceAccount(ctx, adminCreds.AccessKey) })

		newCreds = &drivers.S3Credentials{
			AccessKey: adminCreds.AccessKey,
			SecretKey: adminCreds.SecretKey,
		}
	} else {
		// Handle per-driver implementation for remote storage drivers.
		newCreds, err = b.driver.CreateBucketKey(bucketVol, key.Name, creds, key.Role, op)
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = b.driver.DeleteBucketKey(bucketVol, key.Name, op) })
	}

	key.AccessKey = newCreds.AccessKey
	key.SecretKey = newCreds.SecretKey

	newKey := api.StorageBucketKey{
		Name:        key.Name,
		Description: key.Description,
		Role:        key.Role,
		AccessKey:   key.AccessKey,
		SecretKey:   key.SecretKey,
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err = tx.CreateStoragePoolBucketKey(ctx, bucket.ID, key)

		return err
	})
	if err != nil {
		return nil, err
	}

	revert.Success()
	return &newKey, err
}

// UpdateBucketKey updates bucket key.
func (b *lxdBackend) UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucketName, "keyName": keyName, "desc": key.Description, "role": key.Role})
	l.Debug("UpdateBucketKey started")
	defer l.Debug("UpdateBucketKey finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.Driver().Info().Buckets {
		return fmt.Errorf("Storage pool does not support buckets")
	}

	// Must be defined before revert so that its not cancelled by time revert.Fail runs.
	ctx, ctxCancel := context.WithTimeout(context.TODO(), time.Duration(time.Second*30))
	defer ctxCancel()

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	// Get current config to compare what has changed.
	var bucket *db.StorageBucket
	var curBucketKey *db.StorageBucketKey
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, b.id, projectName, memberSpecific, bucketName)
		if err != nil {
			return err
		}

		curBucketKey, err = tx.GetStoragePoolBucketKey(ctx, bucket.ID, keyName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	curBucketKeyEtagHash, err := util.EtagHash(curBucketKey.Etag())
	if err != nil {
		return err
	}

	newBucketKey := api.StorageBucketKey{
		Name:        curBucketKey.Name,
		Role:        key.Role,
		Description: key.Description,
		AccessKey:   key.AccessKey,
		SecretKey:   key.SecretKey,
	}

	newBucketKeyEtagHash, err := util.EtagHash(newBucketKey.Etag())
	if err != nil {
		return err
	}

	if curBucketKeyEtagHash == newBucketKeyEtagHash {
		return nil // Nothing has changed.
	}

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

	creds := drivers.S3Credentials{
		AccessKey: newBucketKey.AccessKey,
		SecretKey: newBucketKey.SecretKey,
	}

	err = b.driver.ValidateBucketKey(keyName, creds, key.Role)
	if err != nil {
		return err
	}

	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.

		// Start minio process.
		minioProc, err := b.ActivateBucket(projectName, bucket.Name, op)
		if err != nil {
			return err
		}

		bucketPolicy, err := s3.BucketPolicy(bucket.Name, key.Role)
		if err != nil {
			return err
		}

		adminClient, err := minioProc.AdminClient()
		if err != nil {
			return err
		}

		// Delete service account if exists (this allows changing the access key).
		_ = adminClient.DeleteServiceAccount(ctx, curBucketKey.AccessKey)

		newCreds, err := adminClient.AddServiceAccount(ctx, miniod.ServiceAccountArgs{
			Policy:    bucketPolicy,
			AccessKey: creds.AccessKey,
			SecretKey: creds.SecretKey,
		})
		if err != nil {
			return err
		}

		if creds.SecretKey != "" && newCreds.AccessKey != creds.SecretKey {
			// There seems to be a bug in MinIO where if the AccessKey isn't specified for a new
			// service account but a secret key is, *both* the AccessKey and the SecreyKey are randomly
			// generated, even though it should only have been the AccessKey.
			// So detect this and update the SecretKey back to what it should have been.
			err := adminClient.UpdateServiceAccount(ctx, miniod.ServiceAccountArgs{
				AccessKey: newCreds.AccessKey,
				SecretKey: creds.SecretKey,
				Policy:    bucketPolicy, // Ensure policy is also applied.
			})
			if err != nil {
				return err
			}

			newCreds.SecretKey = creds.SecretKey
		}

		key.AccessKey = newCreds.AccessKey
		key.SecretKey = newCreds.SecretKey
	} else {
		// Handle per-driver implementation for remote storage drivers.
		newCreds, err := b.driver.UpdateBucketKey(bucketVol, keyName, creds, key.Role, op)
		if err != nil {
			return err
		}

		key.AccessKey = newCreds.AccessKey
		key.SecretKey = newCreds.SecretKey
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Update the database record.
		return tx.UpdateStoragePoolBucketKey(ctx, bucket.ID, curBucketKey.ID, key)
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteBucketKey deletes an object bucket key.
func (b *lxdBackend) DeleteBucketKey(projectName string, bucketName string, keyName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "bucketName": bucketName, "keyName": keyName})
	l.Debug("DeleteBucketKey started")
	defer l.Debug("DeleteBucketKey finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if !b.Driver().Info().Buckets {
		return fmt.Errorf("Storage pool does not support buckets")
	}

	// Must be defined before revert so that its not cancelled by time revert.Fail runs.
	ctx, ctxCancel := context.WithTimeout(context.TODO(), time.Duration(time.Second*30))
	defer ctxCancel()

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	var bucket *db.StorageBucket
	var bucketKey *db.StorageBucketKey
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, b.id, projectName, memberSpecific, bucketName)
		if err != nil {
			return err
		}

		bucketKey, err = tx.GetStoragePoolBucketKey(ctx, bucket.ID, keyName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.

		// Start minio process.
		minioProc, err := b.ActivateBucket(projectName, bucket.Name, op)
		if err != nil {
			return err
		}

		adminClient, err := minioProc.AdminClient()
		if err != nil {
			return err
		}

		err = adminClient.DeleteServiceAccount(ctx, bucketKey.AccessKey)
		if err != nil {
			return err
		}
	} else {
		// Handle per-driver implementation for remote storage drivers.
		bucketVolName := project.StorageVolume(projectName, bucket.Name)
		bucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config)

		// Delete the bucket key from the storage device.
		err = b.driver.DeleteBucketKey(bucketVol, keyName, op)
		if err != nil {
			return err
		}
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteStoragePoolBucketKey(ctx, bucket.ID, bucketKey.ID)
	})
	if err != nil {
		return fmt.Errorf("Failed deleting bucket key from database: %w", err)
	}

	return nil
}

// ActivateBucket mounts the local bucket volume and returns the MinIO S3 process for it.
func (b *lxdBackend) ActivateBucket(projectName string, bucketName string, _ *operations.Operation) (*miniod.Process, error) {
	if !b.Driver().Info().Buckets {
		return nil, fmt.Errorf("Storage pool does not support buckets")
	}

	if b.Driver().Info().Remote {
		return nil, fmt.Errorf("Remote buckets cannot be activated")
	}

	bucketVolName := project.StorageVolume(projectName, bucketName)
	bucketVol := b.GetVolume(drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, nil)

	return miniod.EnsureRunning(b.state, bucketVol)
}

// GetBucketURL returns S3 URL for bucket.
func (b *lxdBackend) GetBucketURL(bucketName string) *url.URL {
	err := b.isStatusReady()
	if err != nil {
		return nil
	}

	if !b.Driver().Info().Buckets {
		return nil
	}

	memberSpecific := !b.Driver().Info().Remote // Member specific if storage pool isn't remote.

	if memberSpecific {
		// Handle common MinIO implementation for local storage drivers.

		// Check that the storage buckets listener is configured via core.storage_buckets_address.
		storageBucketsAddress := b.state.Endpoints.StorageBucketsAddress()
		if storageBucketsAddress == "" {
			return nil
		}

		return &api.NewURL().Scheme("https").Host(storageBucketsAddress).Path(bucketName).URL
	}

	// Handle per-driver implementation for remote storage drivers.
	return b.driver.GetBucketURL(bucketName)
}

// CreateCustomVolume creates an empty custom volume.
func (b *lxdBackend) CreateCustomVolume(projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "desc": desc, "config": config, "contentType": contentType})
	l.Debug("CreateCustomVolume started")
	defer l.Debug("CreateCustomVolume finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)
	vol := b.GetNewVolume(drivers.VolumeTypeCustom, contentType, volStorageName, config)

	storagePoolSupported := false
	for _, supportedType := range b.Driver().Info().VolumeTypes {
		if supportedType == drivers.VolumeTypeCustom {
			storagePoolSupported = true
			break
		}
	}

	if !storagePoolSupported {
		return fmt.Errorf("Storage pool does not support custom volume type")
	}

	revert := revert.New()
	defer revert.Fail()

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, projectName, volName, desc, vol.Type(), false, vol.Config(), time.Now().UTC(), time.Time{}, vol.ContentType(), false, false)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, projectName, volName, vol.Type()) })

	// Create the empty custom volume on the storage device.
	err = b.driver.CreateVolume(vol, nil, op)
	if err != nil {
		return err
	}

	eventCtx := logger.Ctx{"type": vol.Type()}
	if !b.Driver().Info().Remote {
		eventCtx["location"] = b.state.ServerName
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeCreated.Event(vol, string(vol.Type()), projectName, op, eventCtx))

	revert.Success()
	return nil
}

// CreateCustomVolumeFromCopy creates a custom volume from an existing custom volume.
// It copies the snapshots from the source volume by default, but can be disabled if requested.
func (b *lxdBackend) CreateCustomVolumeFromCopy(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "srcProjectName": srcProjectName, "volName": volName, "desc": desc, "config": config, "srcPoolName": srcPoolName, "srcVolName": srcVolName, "snapshots": snapshots})
	l.Debug("CreateCustomVolumeFromCopy started")
	defer l.Debug("CreateCustomVolumeFromCopy finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	if srcProjectName == "" {
		srcProjectName = projectName
	}

	// Setup the source pool backend instance.
	var srcPool Pool
	if b.name == srcPoolName {
		srcPool = b // Source and target are in the same pool so share pool var.
	} else {
		// Source is in a different pool to target, so load the pool.
		srcPool, err = LoadByName(b.state, srcPoolName)
		if err != nil {
			return err
		}
	}

	// Check source volume exists and is custom type, and get its config including all of the snapshots.
	srcConfig, err := srcPool.GenerateCustomVolumeBackupConfig(srcProjectName, srcVolName, true, op)
	if err != nil {
		return fmt.Errorf("Failed generating volume copy config: %w", err)
	}

	// Use the source volume's config if not supplied.
	if config == nil {
		config = srcConfig.Volume.Config
	}

	// Use the source volume's description if not supplied.
	if desc == "" {
		desc = srcConfig.Volume.Description
	}

	contentDBType, err := cluster.StoragePoolVolumeContentTypeFromName(srcConfig.Volume.ContentType)
	if err != nil {
		return err
	}

	// Get the source volume's content type.
	contentType := VolumeDBContentTypeToContentType(contentDBType)

	storagePoolSupported := false
	for _, supportedType := range b.Driver().Info().VolumeTypes {
		if supportedType == drivers.VolumeTypeCustom {
			storagePoolSupported = true
			break
		}
	}

	if !storagePoolSupported {
		return fmt.Errorf("Storage pool does not support custom volume type")
	}

	// Use the information from the backup config to create a list of all the source volume's snapshots.
	// This way we don't have to retrieve them separately from the database.
	sourceSnapshots := make([]drivers.Volume, 0, len(srcConfig.VolumeSnapshots))
	for _, sourceSnap := range srcConfig.VolumeSnapshots {
		snapshotName := drivers.GetSnapshotVolumeName(srcConfig.Volume.Name, sourceSnap.Name)
		snapshotStorageName := project.StorageVolume(srcProjectName, snapshotName)
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, sourceSnap.Config))
	}

	// Unset the snapshots in the backup config if not requested by the caller.
	// Those were only required to create the list of source volume snapshots.
	if !snapshots {
		srcConfig.VolumeSnapshots = nil
	}

	// If we are copying snapshots, retrieve a list of snapshots from source volume.
	var snapshotNames []string
	if snapshots {
		snapshotNames = make([]string, 0, len(srcConfig.VolumeSnapshots))
		for _, snapshot := range srcConfig.VolumeSnapshots {
			snapshotNames = append(snapshotNames, snapshot.Name)
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Get the src volume name on storage.
	srcVolStorageName := project.StorageVolume(srcProjectName, srcConfig.Volume.Name)
	srcVol := srcPool.GetVolume(drivers.VolumeTypeCustom, contentType, srcVolStorageName, srcConfig.Volume.Config)

	// If the source and target are in the same pool then use CreateVolumeFromCopy rather than
	// migration system as it will be quicker.
	if srcPool == b {
		l.Debug("CreateCustomVolumeFromCopy same-pool mode detected")

		// Get the volume name on storage.
		volStorageName := project.StorageVolume(projectName, volName)
		vol := b.GetNewVolume(drivers.VolumeTypeCustom, contentType, volStorageName, config)

		// Validate config and create database entry for new storage volume.
		err = VolumeDBCreate(b, projectName, volName, desc, vol.Type(), false, vol.Config(), time.Now().UTC(), time.Time{}, vol.ContentType(), false, true)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, projectName, volName, vol.Type()) })

		targetSnapshots := make([]drivers.Volume, 0, len(snapshotNames))

		// Create database entries for new storage volume snapshots.
		for i, snapName := range snapshotNames {
			newSnapshotName := drivers.GetSnapshotVolumeName(volName, snapName)
			var volumeSnapExpiryDate time.Time
			if srcConfig.VolumeSnapshots[i].ExpiresAt != nil {
				volumeSnapExpiryDate = *srcConfig.VolumeSnapshots[i].ExpiresAt
			}

			// Create a new snapshot volume with its own config and UUID.
			snapVol := b.GetNewVolume(vol.Type(), contentType, newSnapshotName, srcConfig.VolumeSnapshots[i].Config)

			// Validate config and create database entry for new storage volume.
			err = VolumeDBCreate(b, projectName, newSnapshotName, srcConfig.VolumeSnapshots[i].Description, vol.Type(), true, snapVol.Config(), srcConfig.VolumeSnapshots[i].CreatedAt, volumeSnapExpiryDate, vol.ContentType(), false, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, projectName, newSnapshotName, vol.Type()) })

			newSnapshotStorageName := project.StorageVolume(projectName, newSnapshotName)
			targetSnapshots = append(targetSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, newSnapshotStorageName, snapVol.Config()))
		}

		volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)
		srcVolCopy := drivers.NewVolumeCopy(srcVol, sourceSnapshots...)

		err = b.driver.CreateVolumeFromCopy(volCopy, srcVolCopy, false, op)
		if err != nil {
			return err
		}

		eventCtx := logger.Ctx{"type": vol.Type()}
		if !b.Driver().Info().Remote {
			eventCtx["location"] = b.state.ServerName
		}

		b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeCreated.Event(vol, string(vol.Type()), projectName, op, eventCtx))

		revert.Success()
		return nil
	}

	// We are copying volumes between storage pools so use migration system as it will be able
	// to negotiate a common transfer method between pool types.
	l.Debug("CreateCustomVolumeFromCopy cross-pool mode detected")

	// Negotiate the migration type to use.
	offeredTypes := srcPool.MigrationTypes(contentType, false, snapshots)
	offerHeader := migration.TypesToHeader(offeredTypes...)
	migrationTypes, err := migration.MatchTypes(offerHeader, FallbackMigrationType(contentType), b.MigrationTypes(contentType, false, snapshots))
	if err != nil {
		return fmt.Errorf("Failed to negotiate copy migration type: %w", err)
	}

	// If we're copying block volumes, the target block volume needs to be
	// at least the size of the source volume, otherwise we'll run into
	// "no space left on device".
	var volSize int64

	if drivers.IsContentBlock(contentType) {
		err = srcVol.MountTask(func(_ string, _ *operations.Operation) error {
			srcPoolBackend, ok := srcPool.(*lxdBackend)
			if !ok {
				return fmt.Errorf("Pool is not a lxdBackend")
			}

			volDiskPath, err := srcPoolBackend.driver.GetVolumeDiskPath(srcVol)
			if err != nil {
				return err
			}

			volSize, err = block.DiskSizeBytes(volDiskPath)
			if err != nil {
				return err
			}

			return nil
		}, nil)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Use in-memory pipe pair to simulate a connection between the sender and receiver.
	aEnd, bEnd := memorypipe.NewPipePair(ctx)

	// Run sender and receiver in separate go routines to prevent deadlocks.
	aEndErrCh := make(chan error, 1)
	bEndErrCh := make(chan error, 1)
	go func() {
		err := srcPool.MigrateCustomVolume(srcProjectName, aEnd, &migration.VolumeSourceArgs{
			IndexHeaderVersion: migration.IndexHeaderVersion,
			Name:               srcConfig.Volume.Name,
			Snapshots:          snapshotNames,
			MigrationType:      migrationTypes[0],
			TrackProgress:      true, // Do use a progress tracker on sender.
			ContentType:        string(contentType),
			Info:               &migration.Info{Config: srcConfig},
			VolumeOnly:         !snapshots,
		}, op)

		if err != nil {
			cancel()
		}

		aEndErrCh <- err
	}()

	go func() {
		err := b.CreateCustomVolumeFromMigration(projectName, bEnd, migration.VolumeTargetArgs{
			IndexHeaderVersion: migration.IndexHeaderVersion,
			Name:               volName,
			Description:        desc,
			Config:             config,
			Snapshots:          snapshotNames,
			MigrationType:      migrationTypes[0],
			TrackProgress:      false, // Do not use a progress tracker on receiver.
			ContentType:        string(contentType),
			VolumeSize:         volSize, // Block size setting override.
			VolumeOnly:         !snapshots,
		}, op)

		if err != nil {
			cancel()
		}

		bEndErrCh <- err
	}()

	// Capture errors from the sender and receiver from their result channels.
	errs := []error{}
	aEndErr := <-aEndErrCh
	if aEndErr != nil {
		_ = aEnd.Close()
		errs = append(errs, aEndErr)
	}

	bEndErr := <-bEndErrCh
	if bEndErr != nil {
		errs = append(errs, bEndErr)
	}

	cancel()

	if len(errs) > 0 {
		return fmt.Errorf("Create custom volume from copy failed: %v", errs)
	}

	revert.Success()
	return nil
}

// migrationIndexHeaderSend sends the migration index header to target and waits for confirmation of receipt.
func (b *lxdBackend) migrationIndexHeaderSend(l logger.Logger, indexHeaderVersion uint32, conn io.ReadWriteCloser, info *migration.Info) (*migration.InfoResponse, error) {
	infoResp := migration.InfoResponse{}

	// Send migration index header frame to target if applicable and wait for receipt.
	if indexHeaderVersion > 0 {
		headerJSON, err := json.Marshal(info)
		if err != nil {
			return nil, fmt.Errorf("Failed encoding migration index header: %w", err)
		}

		_, err = conn.Write(headerJSON)
		if err != nil {
			return nil, fmt.Errorf("Failed sending migration index header: %w", err)
		}

		err = conn.Close() // End the frame.
		if err != nil {
			return nil, fmt.Errorf("Failed closing migration index header frame: %w", err)
		}

		l.Debug("Sent migration index header, waiting for response", logger.Ctx{"version": indexHeaderVersion})

		respBuf, err := io.ReadAll(conn)
		if err != nil {
			return nil, fmt.Errorf("Failed reading migration index header: %w", err)
		}

		err = json.Unmarshal(respBuf, &infoResp)
		if err != nil {
			return nil, fmt.Errorf("Failed decoding migration index header response: %w", err)
		}

		if infoResp.Err() != nil {
			return nil, fmt.Errorf("Failed negotiating migration options: %w", err)
		}

		l.Info("Received migration index header response", logger.Ctx{"response": fmt.Sprintf("%+v", infoResp), "version": indexHeaderVersion})
	}

	return &infoResp, nil
}

// migrationIndexHeaderReceive receives migration index header from source and sends confirmation of receipt.
// Returns the received source index header info.
func (b *lxdBackend) migrationIndexHeaderReceive(l logger.Logger, indexHeaderVersion uint32, conn io.ReadWriteCloser, refresh bool) (*migration.Info, error) {
	info := migration.Info{}

	// Receive index header from source if applicable and respond confirming receipt.
	if indexHeaderVersion > 0 {
		l.Debug("Waiting for migration index header", logger.Ctx{"version": indexHeaderVersion})

		buf, err := io.ReadAll(conn)
		if err != nil {
			return nil, fmt.Errorf("Failed reading migration index header: %w", err)
		}

		err = json.Unmarshal(buf, &info)
		if err != nil {
			return nil, fmt.Errorf("Failed decoding migration index header: %w", err)
		}

		l.Info("Received migration index header, sending response", logger.Ctx{"version": indexHeaderVersion})

		infoResp := migration.InfoResponse{StatusCode: http.StatusOK, Refresh: &refresh}
		headerJSON, err := json.Marshal(infoResp)
		if err != nil {
			return nil, fmt.Errorf("Failed encoding migration index header response: %w", err)
		}

		_, err = conn.Write(headerJSON)
		if err != nil {
			return nil, fmt.Errorf("Failed sending migration index header response: %w", err)
		}

		err = conn.Close() // End the frame.
		if err != nil {
			return nil, fmt.Errorf("Failed closing migration index header response frame: %w", err)
		}

		l.Debug("Sent migration index header response", logger.Ctx{"version": indexHeaderVersion})
	}

	return &info, nil
}

// MigrateCustomVolume sends a volume for migration.
func (b *lxdBackend) MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": args.Name, "args": fmt.Sprintf("%+v", args)})
	l.Debug("MigrateCustomVolume started")
	defer l.Debug("MigrateCustomVolume finished")

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, args.Name)

	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(args.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	if args.Info == nil {
		return fmt.Errorf("Migration info required")
	}

	if args.Info.Config == nil || args.Info.Config.Volume == nil || args.Info.Config.Volume.Config == nil {
		return fmt.Errorf("Volume config is required")
	}

	if len(args.Snapshots) != len(args.Info.Config.VolumeSnapshots) {
		return fmt.Errorf("Requested snapshots count (%d) doesn't match volume snapshot config count (%d)", len(args.Snapshots), len(args.Info.Config.VolumeSnapshots))
	}

	// Send migration index header frame with volume info and wait for receipt.
	resp, err := b.migrationIndexHeaderSend(l, args.IndexHeaderVersion, conn, args.Info)
	if err != nil {
		return err
	}

	if resp.Refresh != nil {
		args.Refresh = *resp.Refresh
	}

	vol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, args.Info.Config.Volume.Config)

	// Retrieve a list of snapshots.
	allSourceSnapshots, err := VolumeDBSnapshotsGet(b, projectName, args.Name, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	sourceSnapshots := make([]drivers.Volume, 0, len(allSourceSnapshots))
	for _, sourceSnapshot := range allSourceSnapshots {
		snapshotStorageName := project.StorageVolume(projectName, sourceSnapshot.Name)
		sourceSnapshots = append(sourceSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, sourceSnapshot.Config))
	}

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	err = b.driver.MigrateVolume(volCopy, conn, args, op)
	if err != nil {
		return err
	}

	return nil
}

// CreateCustomVolumeFromMigration receives a volume being migrated.
func (b *lxdBackend) CreateCustomVolumeFromMigration(projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": args.Name, "args": fmt.Sprintf("%+v", args)})
	l.Debug("CreateCustomVolumeFromMigration started")
	defer l.Debug("CreateCustomVolumeFromMigration finished")

	err := b.isStatusReady()
	if err != nil {
		return err
	}

	storagePoolSupported := false
	for _, supportedType := range b.Driver().Info().VolumeTypes {
		if supportedType == drivers.VolumeTypeCustom {
			storagePoolSupported = true
			break
		}
	}

	if !storagePoolSupported {
		return fmt.Errorf("Storage pool does not support custom volume type")
	}

	var volumeConfig map[string]string

	// Check if the volume exists in database.
	dbVol, err := VolumeDBGet(b, projectName, args.Name, drivers.VolumeTypeCustom)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	// Prefer using existing volume config (to allow mounting existing volume correctly).
	if dbVol != nil {
		volumeConfig = dbVol.Config
	} else {
		volumeConfig = args.Config
	}

	// Check if the volume exists on storage.
	var vol drivers.Volume
	volStorageName := project.StorageVolume(projectName, args.Name)
	if args.Refresh {
		vol = b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(args.ContentType), volStorageName, volumeConfig)
	} else {
		vol = b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentType(args.ContentType), volStorageName, volumeConfig)
	}

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	// Check for inconsistencies between database and storage before continuing.
	if dbVol == nil && volExists {
		return fmt.Errorf("Volume already exists on storage but not in database")
	}

	if dbVol != nil && !volExists {
		return fmt.Errorf("Volume exists in database but not on storage")
	}

	// Disable refresh mode if volume doesn't exist yet.
	// Unlike in CreateInstanceFromMigration there is no existing check for if the volume exists, so we must do
	// it here and disable refresh mode if the volume doesn't exist.
	if args.Refresh && !volExists {
		args.Refresh = false
	} else if !args.Refresh && volExists {
		return fmt.Errorf("Cannot create volume, already exists on migration target storage")
	}

	// VolumeSize is set to the actual size of the underlying block device.
	// The target should use this value if present, otherwise it might get an error like
	// "no space left on device".
	if args.VolumeSize > 0 {
		vol.SetConfigSize(strconv.FormatInt(args.VolumeSize, 10))
	}

	// Receive index header from source if applicable and respond confirming receipt.
	// This will also let the source know whether to actually perform a refresh, as the target
	// will set Refresh to false if the volume doesn't exist.
	srcInfo, err := b.migrationIndexHeaderReceive(l, args.IndexHeaderVersion, conn, args.Refresh)
	if err != nil {
		return err
	}

	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, projectName)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	revert := revert.New()
	defer revert.Fail()

	if !args.Refresh {
		// Validate config and create database entry for new storage volume.
		// Strip unsupported config keys (in case the export was made from a different type of storage pool).
		err = VolumeDBCreate(b, projectName, args.Name, args.Description, vol.Type(), false, vol.Config(), time.Now().UTC(), time.Time{}, vol.ContentType(), true, true)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, projectName, args.Name, vol.Type()) })
	}

	if len(args.Snapshots) > 0 {
		// Create database entries for new storage volume snapshots.
		for _, snapName := range args.Snapshots {
			newSnapshotName := drivers.GetSnapshotVolumeName(args.Name, snapName)

			snapConfig := vol.Config() // Use parent volume config by default.
			snapDescription := args.Description
			snapExpiryDate := time.Time{}
			snapCreationDate := time.Time{}

			// If the source snapshot config is available, use that.
			if srcInfo != nil && srcInfo.Config != nil {
				for _, srcSnap := range srcInfo.Config.VolumeSnapshots {
					if srcSnap.Name != snapName {
						continue
					}

					snapConfig = srcSnap.Config
					snapDescription = srcSnap.Description

					if srcSnap.ExpiresAt != nil {
						snapExpiryDate = *srcSnap.ExpiresAt
					}

					snapCreationDate = srcSnap.CreatedAt

					break
				}
			}

			// Create a new snapshot volume with its own config and UUID.
			snapVol := b.GetNewVolume(vol.Type(), vol.ContentType(), newSnapshotName, snapConfig)

			// Validate config and create database entry for new storage volume.
			// Strip unsupported config keys (in case the export was made from a different type of storage pool).
			err = VolumeDBCreate(b, projectName, newSnapshotName, snapDescription, vol.Type(), true, snapVol.Config(), snapCreationDate, snapExpiryDate, vol.ContentType(), true, true)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = VolumeDBDelete(b, projectName, newSnapshotName, vol.Type()) })
		}
	}

	// Retrieve a list of target volume snapshots.
	allTargetSnapshots, err := VolumeDBSnapshotsGet(b, projectName, args.Name, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	targetSnapshots := make([]drivers.Volume, 0, len(allTargetSnapshots))
	for _, targetSnapshot := range allTargetSnapshots {
		snapshotStorageName := project.StorageVolume(projectName, targetSnapshot.Name)
		targetSnapshots = append(targetSnapshots, b.GetVolume(drivers.VolumeTypeCustom, vol.ContentType(), snapshotStorageName, targetSnapshot.Config))
	}

	volCopy := drivers.NewVolumeCopy(vol, targetSnapshots...)

	err = b.driver.CreateVolumeFromMigration(volCopy, conn, args, nil, op)
	if err != nil {
		return err
	}

	eventCtx := logger.Ctx{"type": vol.Type()}
	if !b.Driver().Info().Remote {
		eventCtx["location"] = b.state.ServerName
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeCreated.Event(vol, string(vol.Type()), projectName, op, eventCtx))

	revert.Success()
	return nil
}

// RenameCustomVolume renames a custom volume and its snapshots.
func (b *lxdBackend) RenameCustomVolume(projectName string, volName string, newVolName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "newVolName": newVolName})
	l.Debug("RenameCustomVolume started")
	defer l.Debug("RenameCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	if shared.IsSnapshot(newVolName) {
		return fmt.Errorf("New volume name cannot be a snapshot")
	}

	revert := revert.New()
	defer revert.Fail()

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Rename each snapshot to have the new parent volume prefix.
	snapshots, err := VolumeDBSnapshotsGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	for _, srcSnapshot := range snapshots {
		_, snapName, _ := api.GetParentAndSnapshotName(srcSnapshot.Name)
		newSnapVolName := drivers.GetSnapshotVolumeName(newVolName, snapName)
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.RenameStoragePoolVolume(ctx, projectName, srcSnapshot.Name, newSnapVolName, cluster.StoragePoolVolumeTypeCustom, b.ID())
		})
		if err != nil {
			return err
		}

		revert.Add(func() {
			_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.RenameStoragePoolVolume(ctx, projectName, newSnapVolName, srcSnapshot.Name, cluster.StoragePoolVolumeTypeCustom, b.ID())
			})
		})
	}

	var backups []db.StoragePoolVolumeBackup

	// Rename each backup to have the new parent volume prefix.
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		backups, err = tx.GetStoragePoolVolumeBackups(ctx, projectName, volName, b.ID())
		return err
	})
	if err != nil {
		return err
	}

	for _, br := range backups {
		backupRow := br // Local var for revert.
		_, backupName, _ := api.GetParentAndSnapshotName(backupRow.Name)
		newVolBackupName := drivers.GetSnapshotVolumeName(newVolName, backupName)
		volBackup := backup.NewVolumeBackup(b.state, projectName, b.name, volName, backupRow.ID, backupRow.Name, backupRow.CreationDate, backupRow.ExpiryDate, backupRow.VolumeOnly, backupRow.OptimizedStorage)
		err = volBackup.Rename(newVolBackupName)
		if err != nil {
			return fmt.Errorf("Failed renaming backup %q to %q: %w", backupRow.Name, newVolBackupName, err)
		}

		revert.Add(func() {
			_ = volBackup.Rename(backupRow.Name)
		})
	}

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameStoragePoolVolume(ctx, projectName, volName, newVolName, cluster.StoragePoolVolumeTypeCustom, b.ID())
	})
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.RenameStoragePoolVolume(ctx, projectName, newVolName, volName, cluster.StoragePoolVolumeTypeCustom, b.ID())
		})
	})

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)
	newVolStorageName := project.StorageVolume(projectName, newVolName)

	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	err = b.driver.RenameVolume(vol, newVolStorageName, op)
	if err != nil {
		return err
	}

	vol = b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), newVolStorageName, nil)
	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeRenamed.Event(vol, string(vol.Type()), projectName, op, logger.Ctx{"old_name": volName}))

	revert.Success()
	return nil
}

// detectChangedConfig returns the config that has changed between current and new config maps.
// Also returns a boolean indicating whether all of the changed keys start with "user.".
// Deleted keys will be returned as having an empty string value.
func (b *lxdBackend) detectChangedConfig(curConfig, newConfig map[string]string) (map[string]string, bool) {
	// Diff the configurations.
	changedConfig := make(map[string]string)
	userOnly := true
	for key := range curConfig {
		if curConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			changedConfig[key] = newConfig[key] // Will be empty string on deleted keys.
		}
	}

	for key := range newConfig {
		if curConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			changedConfig[key] = newConfig[key]
		}
	}

	return changedConfig, userOnly
}

func allowRemoveSecurityShared(s *state.State, projectName string, volume *api.StorageVolume) error {
	err := VolumeUsedByProfileDevices(s, volume.Pool, projectName, volume, func(_ int64, _ api.Profile, _ api.Project, _ []string) error {
		return errors.New("Cannot disable security.shared on block storage volume as it is attached to profile(s)")
	})
	if err != nil {
		return err
	}

	usedByInstances := 0

	err = VolumeUsedByInstanceDevices(s, volume.Pool, projectName, volume, true, func(inst db.InstanceArgs, _ api.Project, _ []string) error {
		// Don't consider a virtual-machine to be using its root volume if security.protection.start=true
		if volume.Type == cluster.StoragePoolVolumeTypeNameVM && inst.Type == instancetype.VM && volume.Project == inst.Project && volume.Name == inst.Name {
			apiInst, err := inst.ToAPI()
			if err != nil {
				return err
			}

			apiInst.ExpandedConfig = instancetype.ExpandInstanceConfig(s.GlobalConfig.Dump(), apiInst.Config, inst.Profiles)

			if shared.IsTrue(apiInst.ExpandedConfig["security.protection.start"]) {
				return nil
			}
		}

		usedByInstances++

		if usedByInstances > 1 {
			return errors.New("Cannot disable security.shared on block storage volume as it is attached to more than one instance")
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateCustomVolume applies the supplied config to the custom volume.
func (b *lxdBackend) UpdateCustomVolume(projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "newDesc": newDesc, "newConfig": newConfig})
	l.Debug("UpdateCustomVolume started")
	defer l.Debug("UpdateCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	// Get current config to compare what has changed.
	curVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Get content type.
	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(curVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	// Validate config.
	newVol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, newConfig)
	err = b.driver.ValidateVolume(newVol, false)
	if err != nil {
		return err
	}

	// Apply config changes if there are any.
	changedConfig, userOnly := b.detectChangedConfig(curVol.Config, newConfig)
	if len(changedConfig) != 0 {
		// Forbid changing the config for ISO custom volumes as they are read-only.
		if contentType == drivers.ContentTypeISO {
			return fmt.Errorf("Custom ISO volume config cannot be changed")
		}

		// Check that the volume's block.filesystem property isn't being changed.
		if changedConfig["block.filesystem"] != "" {
			return fmt.Errorf(`Custom volume "block.filesystem" property cannot be changed`)
		}

		// Check that the volume's volatile.uuid property isn't being changed.
		if changedConfig["volatile.uuid"] != "" {
			return fmt.Errorf(`Custom volume "volatile.uuid" property cannot be changed`)
		}

		// Check for config changing that is not allowed when running instances are using it.
		if changedConfig["security.shifted"] != "" {
			err = VolumeUsedByInstanceDevices(b.state, b.name, projectName, &curVol.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, _ []string) error {
				inst, err := instance.Load(b.state, dbInst, project)
				if err != nil {
					return err
				}

				// Confirm that no running instances are using it when changing shifted state.
				if inst.IsRunning() && changedConfig["security.shifted"] != "" {
					return fmt.Errorf("Cannot modify shifting with running instances using the volume")
				}

				return nil
			})
			if err != nil {
				return err
			}
		}

		sharedVolume, ok := changedConfig["security.shared"]
		if ok && shared.IsFalseOrEmpty(sharedVolume) && curVol.ContentType == cluster.StoragePoolVolumeContentTypeNameBlock {
			err = allowRemoveSecurityShared(b.state, projectName, &curVol.StorageVolume)
			if err != nil {
				return err
			}
		}

		curVol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, curVol.Config)
		if !userOnly {
			err = b.driver.UpdateVolume(curVol, changedConfig)
			if err != nil {
				return err
			}
		}
	}

	// Unset idmap keys if volume is unmapped.
	if shared.IsTrue(newConfig["security.unmapped"]) {
		delete(newConfig, "volatile.idmap.last")
		delete(newConfig, "volatile.idmap.next")
	}

	// Update the database if something changed.
	if len(changedConfig) != 0 || newDesc != curVol.Description {
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStoragePoolVolume(ctx, projectName, volName, cluster.StoragePoolVolumeTypeCustom, b.ID(), newDesc, newConfig)
		})
		if err != nil {
			return err
		}
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeUpdated.Event(newVol, string(newVol.Type()), projectName, op, nil))

	return nil
}

// UpdateCustomVolumeSnapshot updates the description of a custom volume snapshot.
// Volume config is not allowed to be updated and will return an error.
func (b *lxdBackend) UpdateCustomVolumeSnapshot(projectName string, volName string, newDesc string, newConfig map[string]string, newExpiryDate time.Time, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "newDesc": newDesc, "newConfig": newConfig, "newExpiryDate": newExpiryDate})
	l.Debug("UpdateCustomVolumeSnapshot started")
	defer l.Debug("UpdateCustomVolumeSnapshot finished")

	if !shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume must be a snapshot")
	}

	// Get current config to compare what has changed.
	curVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	var curExpiryDate time.Time

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		curExpiryDate, err = tx.GetStorageVolumeSnapshotExpiry(ctx, curVol.ID)

		return err
	})
	if err != nil {
		return err
	}

	if newConfig != nil {
		changedConfig, _ := b.detectChangedConfig(curVol.Config, newConfig)
		if len(changedConfig) != 0 {
			return fmt.Errorf("Volume config is not editable")
		}
	}

	// Update the database if description changed. Use current config.
	if newDesc != curVol.Description || newExpiryDate != curExpiryDate {
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateStorageVolumeSnapshot(ctx, projectName, volName, cluster.StoragePoolVolumeTypeCustom, b.ID(), newDesc, curVol.Config, newExpiryDate)
		})
		if err != nil {
			return err
		}
	}

	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(curVol.ContentType), curVol.Name, curVol.Config)
	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeSnapshotUpdated.Event(vol, string(vol.Type()), projectName, op, nil))

	return nil
}

// DeleteCustomVolume removes a custom volume and its snapshots.
func (b *lxdBackend) DeleteCustomVolume(projectName string, volName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName})
	l.Debug("DeleteCustomVolume started")
	defer l.Debug("DeleteCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	// Retrieve a list of snapshots.
	snapshots, err := VolumeDBSnapshotsGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Remove each snapshot.
	for _, snapshot := range snapshots {
		err = b.DeleteCustomVolumeSnapshot(projectName, snapshot.Name, op)
		if err != nil {
			return err
		}
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	// Get the volume.
	curVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Get the content type.
	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(curVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	vol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, curVol.Config)

	// Delete the volume from the storage device. Must come after snapshots are removed.
	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		err = b.driver.DeleteVolume(vol, op)
		if err != nil {
			return err
		}
	}

	// Remove backups directory for volume.
	backupsPath := shared.VarPath("backups", "custom", b.name, project.StorageVolume(projectName, volName))
	if shared.PathExists(backupsPath) {
		err := os.RemoveAll(backupsPath)
		if err != nil {
			return err
		}
	}

	// Finally, remove the volume record from the database.
	err = VolumeDBDelete(b, projectName, volName, vol.Type())
	if err != nil {
		return err
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeDeleted.Event(vol, string(vol.Type()), projectName, op, nil))

	return nil
}

// GetCustomVolumeUsage returns the disk space used by the custom volume.
func (b *lxdBackend) GetCustomVolumeUsage(projectName, volName string) (*VolumeUsage, error) {
	err := b.isStatusReady()
	if err != nil {
		return nil, err
	}

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return nil, err
	}

	val := VolumeUsage{}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	// Get the usage.
	usedBytes, err := b.driver.GetVolumeUsage(vol)
	if err != nil && !errors.Is(err, drivers.ErrNotSupported) {
		return nil, err
	}

	// If retrieving usage is unsupported, Used should be 0.
	if usedBytes > 0 {
		val.Used = usedBytes
	}

	// Get the total size.
	sizeStr, ok := vol.Config()["size"]
	if ok {
		total, err := units.ParseByteSizeString(sizeStr)
		if err != nil {
			return nil, err
		}

		if total >= 0 {
			val.Total = total
		}
	}

	return &val, nil
}

// MountCustomVolume mounts a custom volume.
func (b *lxdBackend) MountCustomVolume(projectName, volName string, op *operations.Operation) (*MountInfo, error) {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName})
	l.Debug("MountCustomVolume started")
	defer l.Debug("MountCustomVolume finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, err
	}

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return nil, err
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)
	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	// Perform the mount.
	mountInfo := &MountInfo{}
	err = b.driver.MountVolume(vol, op)
	if err != nil {
		return nil, err
	}

	// Handle delegation.
	if b.driver.CanDelegateVolume(vol) {
		mountInfo.PostHooks = append(mountInfo.PostHooks, func(inst instance.Instance) error {
			pid := inst.InitPID()

			// Only apply to running instances.
			if pid < 1 {
				return nil
			}

			return b.driver.DelegateVolume(vol, pid)
		})
	}

	return mountInfo, nil
}

// UnmountCustomVolume unmounts a custom volume.
func (b *lxdBackend) UnmountCustomVolume(projectName, volName string, op *operations.Operation) (bool, error) {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName})
	l.Debug("UnmountCustomVolume started")
	defer l.Debug("UnmountCustomVolume finished")

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return false, err
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)
	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	return b.driver.UnmountVolume(vol, false, op)
}

// ImportCustomVolume takes an existing custom volume on the storage backend and ensures that the DB records,
// volume directories and symlinks are restored as needed to make it operational with LXD.
// Used during the recovery import stage.
func (b *lxdBackend) ImportCustomVolume(projectName string, poolVol *backupConfig.Config, _ *operations.Operation) (revert.Hook, error) {
	if poolVol.Volume == nil {
		return nil, fmt.Errorf("Invalid pool volume config supplied")
	}

	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": poolVol.Volume.Name})
	l.Debug("ImportCustomVolume started")
	defer l.Debug("ImportCustomVolume finished")

	revert := revert.New()
	defer revert.Fail()

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, poolVol.Volume.Name)

	// Copy volume config from backup file if present (so VolumeDBCreate can safely modify the copy if needed).
	vol := b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentType(poolVol.Volume.ContentType), volStorageName, poolVol.Volume.Config)

	// Validate config and create database entry for restored storage volume.
	err := VolumeDBCreate(b, projectName, poolVol.Volume.Name, poolVol.Volume.Description, drivers.VolumeTypeCustom, false, vol.Config(), poolVol.Volume.CreatedAt, time.Time{}, drivers.ContentType(poolVol.Volume.ContentType), false, true)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, projectName, poolVol.Volume.Name, drivers.VolumeTypeCustom) })

	// Create the storage volume snapshot DB records.
	for _, poolVolSnap := range poolVol.VolumeSnapshots {
		fullSnapName := drivers.GetSnapshotVolumeName(poolVol.Volume.Name, poolVolSnap.Name)

		// Copy volume config from backup file if present
		// (so VolumeDBCreate can safely modify the copy if needed).
		snapVol := b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentType(poolVolSnap.ContentType), fullSnapName, poolVolSnap.Config)

		// Validate config and create database entry for restored storage volume.
		err = VolumeDBCreate(b, projectName, fullSnapName, poolVolSnap.Description, drivers.VolumeTypeCustom, true, snapVol.Config(), poolVolSnap.CreatedAt, time.Time{}, drivers.ContentType(poolVolSnap.ContentType), false, true)
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, projectName, fullSnapName, drivers.VolumeTypeCustom) })
	}

	// Create the mount path if needed.
	err = vol.EnsureMountPath()
	if err != nil {
		return nil, err
	}

	// Create snapshot mount paths and snapshot parent directory if needed.
	for _, poolVolSnap := range poolVol.VolumeSnapshots {
		l.Debug("Ensuring instance snapshot mount path", logger.Ctx{"snapshot": poolVolSnap.Name})

		snapVol, err := vol.NewSnapshot(poolVolSnap.Name)
		if err != nil {
			return nil, err
		}

		err = snapVol.EnsureMountPath()
		if err != nil {
			return nil, err
		}
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, err
}

// CreateCustomVolumeSnapshot creates a snapshot of a custom volume.
func (b *lxdBackend) CreateCustomVolumeSnapshot(projectName, volName string, newSnapshotName string, newDescription string, newExpiryDate time.Time, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "newSnapshotName": newSnapshotName, "newDescription": newDescription, "newExpiryDate": newExpiryDate})
	l.Debug("CreateCustomVolumeSnapshot started")
	defer l.Debug("CreateCustomVolumeSnapshot finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume does not support snapshots")
	}

	if shared.IsSnapshot(newSnapshotName) {
		return fmt.Errorf("Snapshot name is not a valid snapshot name")
	}

	fullSnapshotName := drivers.GetSnapshotVolumeName(volName, newSnapshotName)

	// Check snapshot volume doesn't exist already.
	volume, err := VolumeDBGet(b, projectName, fullSnapshotName, drivers.VolumeTypeCustom)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	} else if volume != nil {
		return api.StatusErrorf(http.StatusConflict, "Snapshot by that name already exists")
	}

	// Load parent volume information and check it exists.
	parentVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		if response.IsNotFoundError(err) {
			return api.StatusErrorf(http.StatusNotFound, "Parent volume doesn't exist")
		}

		return err
	}

	volDBContentType, err := cluster.StoragePoolVolumeContentTypeFromName(parentVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(volDBContentType)

	if contentType != drivers.ContentTypeFS && contentType != drivers.ContentTypeBlock {
		return fmt.Errorf("Volume of content type %q does not support snapshots", contentType)
	}

	parentUUID := parentVol.Config["volatile.uuid"]
	if parentUUID == "" {
		return fmt.Errorf(`Volume %q is missing the required "volatile.uuid" setting`, parentVol.Name)
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, fullSnapshotName)
	vol := b.GetNewVolume(drivers.VolumeTypeCustom, contentType, volStorageName, parentVol.Config)

	// Set the parent volume's UUID.
	vol.SetParentUUID(parentUUID)

	revert := revert.New()
	defer revert.Fail()

	// Set the description if it's not empty.
	description := parentVol.Description
	if newDescription != "" {
		description = newDescription
	}

	// Lock this operation to ensure that the only one snapshot is made at the time.
	// Other operations will wait for this one to finish.
	unlock, err := locking.Lock(context.TODO(), drivers.OperationLockName("CreateCustomVolumeSnapshot", b.name, vol.Type(), contentType, volName))
	if err != nil {
		return err
	}

	defer unlock()

	// Validate config and create database entry for new storage volume.
	// Copy volume config from parent.
	err = VolumeDBCreate(b, projectName, fullSnapshotName, description, drivers.VolumeTypeCustom, true, vol.Config(), time.Now().UTC(), newExpiryDate, drivers.ContentType(parentVol.ContentType), false, true)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, projectName, fullSnapshotName, drivers.VolumeTypeCustom) })

	// Create the snapshot on the storage device.
	err = b.driver.CreateVolumeSnapshot(vol, op)
	if err != nil {
		return err
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeSnapshotCreated.Event(vol, string(vol.Type()), projectName, op, logger.Ctx{"type": vol.Type()}))

	revert.Success()
	return nil
}

// RenameCustomVolumeSnapshot renames a custom volume.
func (b *lxdBackend) RenameCustomVolumeSnapshot(projectName, volName string, newSnapshotName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "newSnapshotName": newSnapshotName})
	l.Debug("RenameCustomVolumeSnapshot started")
	defer l.Debug("RenameCustomVolumeSnapshot finished")

	parentName, oldSnapshotName, isSnap := api.GetParentAndSnapshotName(volName)
	if !isSnap {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	if shared.IsSnapshot(newSnapshotName) {
		return fmt.Errorf("Invalid new snapshot name")
	}

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	err = b.driver.RenameVolumeSnapshot(vol, newSnapshotName, op)
	if err != nil {
		return err
	}

	newVolName := drivers.GetSnapshotVolumeName(parentName, newSnapshotName)
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameStoragePoolVolume(ctx, projectName, volName, newVolName, cluster.StoragePoolVolumeTypeCustom, b.ID())
	})
	if err != nil {
		// Get the volume name on storage.
		newVolStorageName := project.StorageVolume(projectName, newVolName)

		// Revert rename.
		// Renaming a volume snapshot doesn't change its UUID.
		// Pass the same configuration as for the initial rename operation.
		newVol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), newVolStorageName, volume.Config)
		_ = b.driver.RenameVolumeSnapshot(newVol, oldSnapshotName, op)
		return err
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeSnapshotRenamed.Event(vol, string(vol.Type()), projectName, op, logger.Ctx{"old_name": oldSnapshotName}))

	return nil
}

// DeleteCustomVolumeSnapshot removes a custom volume snapshot.
func (b *lxdBackend) DeleteCustomVolumeSnapshot(projectName, volName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName})
	l.Debug("DeleteCustomVolumeSnapshot started")
	defer l.Debug("DeleteCustomVolumeSnapshot finished")

	isSnap := shared.IsSnapshot(volName)

	if !isSnap {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	// Get the volume.
	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Get the content type.
	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(volume.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	vol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, volume.Config)

	// Set the parent volume's UUID.
	if b.driver.Info().PopulateParentVolumeUUID {
		parentUUID, err := b.getParentVolumeUUID(vol, projectName)
		if err != nil {
			return err
		}

		vol.SetParentUUID(parentUUID)
	}

	// Delete the snapshot from the storage device.
	// Must come before DB VolumeDBDelete so that the volume ID is still available.
	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		err := b.driver.DeleteVolumeSnapshot(vol, op)
		if err != nil {
			return err
		}
	}

	// Remove the snapshot volume record from the database.
	err = VolumeDBDelete(b, projectName, volName, vol.Type())
	if err != nil {
		return err
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeSnapshotDeleted.Event(vol, string(vol.Type()), projectName, op, nil))

	return nil
}

// RestoreCustomVolume restores a custom volume from a snapshot.
func (b *lxdBackend) RestoreCustomVolume(projectName, volName string, snapshotName string, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volName": volName, "snapshotName": snapshotName})
	l.Debug("RestoreCustomVolume started")
	defer l.Debug("RestoreCustomVolume finished")

	// Quick checks.
	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume cannot be snapshot")
	}

	if shared.IsSnapshot(snapshotName) {
		return fmt.Errorf("Invalid snapshot name")
	}

	// Get current volume.
	curVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Check that the volume isn't in use by running instances.
	err = VolumeUsedByInstanceDevices(b.state, b.Name(), projectName, &curVol.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, _ []string) error {
		inst, err := instance.Load(b.state, dbInst, project)
		if err != nil {
			return err
		}

		if inst.IsRunning() {
			return fmt.Errorf("Cannot restore custom volume used by running instances")
		}

		return nil
	})
	if err != nil {
		return err
	}

	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(curVol.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(dbContentType)

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)
	vol := b.GetVolume(drivers.VolumeTypeCustom, contentType, volStorageName, curVol.Config)

	// Get the volume's snapshot from the DB.
	fullSnapshotName := drivers.GetSnapshotVolumeName(volName, snapshotName)
	dbSnapVol, err := VolumeDBGet(b, projectName, fullSnapshotName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	snapshotStorageName := project.StorageVolume(projectName, dbSnapVol.Name)
	snapVol := b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, dbSnapVol.Config)

	err = b.driver.RestoreVolume(vol, snapVol, op)
	if err != nil {
		snapErr, ok := err.(drivers.ErrDeleteSnapshots)
		if ok {
			// We need to delete some snapshots and try again.
			for _, snapName := range snapErr.Snapshots {
				err := b.DeleteCustomVolumeSnapshot(projectName, volName+"/"+snapName, op)
				if err != nil {
					return err
				}
			}

			// Now try again.
			err = b.driver.RestoreVolume(vol, snapVol, op)
			if err != nil {
				return err
			}
		}

		return err
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeRestored.Event(vol, string(vol.Type()), projectName, op, logger.Ctx{"snapshot": snapshotName}))

	return nil
}

func (b *lxdBackend) createStorageStructure(path string) error {
	for _, volType := range b.driver.Info().VolumeTypes {
		for _, name := range drivers.BaseDirectories[volType] {
			path := filepath.Join(path, name)
			err := os.MkdirAll(path, 0711)
			if err != nil && !os.IsExist(err) {
				return fmt.Errorf("Failed to create directory %q: %w", path, err)
			}
		}
	}

	return nil
}

// GenerateCustomVolumeBackupConfig returns the backup config entry for this volume.
func (b *lxdBackend) GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, _ *operations.Operation) (*backupConfig.Config, error) {
	vol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return nil, err
	}

	if vol.Type != cluster.StoragePoolVolumeTypeNameCustom {
		return nil, fmt.Errorf("Unsupported volume type %q", vol.Type)
	}

	config := &backupConfig.Config{
		Volume: &vol.StorageVolume,
	}

	if snapshots {
		dbVolSnaps, err := VolumeDBSnapshotsGet(b, projectName, vol.Name, drivers.VolumeTypeCustom)
		if err != nil {
			return nil, err
		}

		config.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(dbVolSnaps))
		for i := range dbVolSnaps {
			_, snapName, _ := api.GetParentAndSnapshotName(dbVolSnaps[i].Name)

			snapshot := api.StorageVolumeSnapshot{
				Name:        snapName, // Snapshot only name, not full name.
				Description: dbVolSnaps[i].Description,
				ExpiresAt:   &dbVolSnaps[i].ExpiryDate,
				Config:      dbVolSnaps[i].Config,
				ContentType: dbVolSnaps[i].ContentType,
				CreatedAt:   dbVolSnaps[i].CreationDate,
			}

			config.VolumeSnapshots = append(config.VolumeSnapshots, &snapshot)
		}
	}

	return config, nil
}

// GenerateInstanceBackupConfig returns the backup config entry for this instance.
// The Container field is only populated for non-snapshot instances.
func (b *lxdBackend) GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, _ *operations.Operation) (*backupConfig.Config, error) {
	// Generate the YAML.
	ci, _, err := inst.Render()
	if err != nil {
		return nil, fmt.Errorf("Failed to render instance metadata: %w", err)
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	volume, err := VolumeDBGet(b, inst.Project().Name, inst.Name(), volType)
	if err != nil {
		return nil, err
	}

	config := &backupConfig.Config{
		Pool:   &b.db,
		Volume: &volume.StorageVolume,
	}

	// Add profiles from instance.
	instProfiles := inst.Profiles()
	config.Profiles = make([]*api.Profile, len(instProfiles))
	for i := range instProfiles {
		config.Profiles[i] = &instProfiles[i]
	}

	// Only populate Container field for non-snapshot instances.
	if !inst.IsSnapshot() {
		var ok bool
		config.Container, ok = ci.(*api.Instance)
		if !ok {
			return nil, fmt.Errorf("Failed to cast %q into its API representation", inst.Name())
		}

		if snapshots {
			snapshots, err := inst.Snapshots()
			if err != nil {
				return nil, fmt.Errorf("Failed to get snapshots: %w", err)
			}

			config.Snapshots = make([]*api.InstanceSnapshot, 0, len(snapshots))
			for _, s := range snapshots {
				si, _, err := s.Render()
				if err != nil {
					return nil, err
				}

				config.Snapshots = append(config.Snapshots, si.(*api.InstanceSnapshot))
			}

			dbVolSnaps, err := VolumeDBSnapshotsGet(b, inst.Project().Name, inst.Name(), volType)
			if err != nil {
				return nil, err
			}

			if len(snapshots) != len(dbVolSnaps) {
				return nil, fmt.Errorf("Instance snapshot record count doesn't match instance snapshot volume record count")
			}

			config.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(dbVolSnaps))
			for i := range dbVolSnaps {
				foundInstanceSnapshot := false
				for _, snap := range snapshots {
					if snap.Name() == dbVolSnaps[i].Name {
						foundInstanceSnapshot = true
						break
					}
				}

				if !foundInstanceSnapshot {
					return nil, fmt.Errorf("Instance snapshot record missing for %q", dbVolSnaps[i].Name)
				}

				_, snapName, _ := api.GetParentAndSnapshotName(dbVolSnaps[i].Name)

				config.VolumeSnapshots = append(config.VolumeSnapshots, &api.StorageVolumeSnapshot{
					Name:        snapName,
					Description: dbVolSnaps[i].Description,
					ExpiresAt:   &dbVolSnaps[i].ExpiryDate,
					Config:      dbVolSnaps[i].Config,
					ContentType: dbVolSnaps[i].ContentType,
					CreatedAt:   dbVolSnaps[i].CreationDate,
				})
			}
		}
	}

	return config, nil
}

// UpdateInstanceBackupFile writes the instance's config to the backup.yaml file on the storage device.
func (b *lxdBackend) UpdateInstanceBackupFile(inst instance.Instance, snapshots bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("UpdateInstanceBackupFile started")
	defer l.Debug("UpdateInstanceBackupFile finished")

	// We only write backup files out for actual instances.
	if inst.IsSnapshot() {
		return nil
	}

	config, err := b.GenerateInstanceBackupConfig(inst, snapshots, op)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	// Get the volume name on storage.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	contentType := InstanceContentType(inst)
	vol := b.GetVolume(volType, contentType, volStorageName, config.Volume.Config)

	// Only need to activate and mount the VM's config volume.
	if inst.Type() == instancetype.VM {
		vol = vol.NewVMBlockFilesystemVolume()
	}

	// Update pool information in the backup.yaml file.
	err = vol.MountTask(func(_ string, _ *operations.Operation) error {
		// Write the YAML
		path := filepath.Join(inst.Path(), "backup.yaml")
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("Failed to create file %q: %w", path, err)
		}

		err = f.Chmod(0400)
		if err != nil {
			return err
		}

		err = shared.WriteAll(f, data)
		if err != nil {
			return err
		}

		return f.Close()
	}, op)

	return err
}

// CheckInstanceBackupFileSnapshots compares the snapshots on the storage device to those defined in the backup
// config supplied and returns an error if they do not match.
func (b *lxdBackend) CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, op *operations.Operation) ([]*api.InstanceSnapshot, error) {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "instance": backupConf.Container.Name})
	l.Debug("CheckInstanceBackupFileSnapshots started")
	defer l.Debug("CheckInstanceBackupFileSnapshots finished")

	instType, err := instancetype.New(string(backupConf.Container.Type))
	if err != nil {
		return nil, err
	}

	volType, err := InstanceTypeToVolumeType(instType)
	if err != nil {
		return nil, err
	}

	// Get the volume name on storage.
	volStorageName := project.Instance(projectName, backupConf.Container.Name)

	contentType := drivers.ContentTypeFS
	if volType == drivers.VolumeTypeVM {
		contentType = drivers.ContentTypeBlock
	}

	// Use the volume's config from the backup config.
	// Some storage drivers might require the UUID to generate the volume name.
	vol := b.GetVolume(volType, contentType, volStorageName, backupConf.Volume.Config)

	// Get a list of snapshots that exist on storage device.
	driverSnapshots, err := vol.Snapshots(op)
	if err != nil {
		return nil, err
	}

	if len(backupConf.Snapshots) != len(driverSnapshots) {
		return nil, fmt.Errorf("Snapshot count in backup config and storage device are different: %w", ErrBackupSnapshotsMismatch)
	}

	volSnaps := make([]drivers.Volume, 0, len(backupConf.VolumeSnapshots))
	for _, snap := range backupConf.VolumeSnapshots {
		snapName := drivers.GetSnapshotVolumeName(backupConf.Container.Name, snap.Name)
		volSnaps = append(volSnaps, b.GetVolume(volType, contentType, snapName, snap.Config))
	}

	err = b.driver.CheckVolumeSnapshots(vol, volSnaps, op)
	if err != nil {
		return nil, err
	}

	return backupConf.Snapshots, nil
}

// ListUnknownVolumes returns volumes that exist on the storage pool but don't have records in the database.
// Returns the unknown volumes parsed/generated backup config in a slice (keyed on project name).
func (b *lxdBackend) ListUnknownVolumes(op *operations.Operation) (map[string][]*backupConfig.Config, error) {
	// Get a list of volumes on the storage pool. We only expect to get 1 volume per logical LXD volume.
	// So for VMs we only expect to get the block volume for a VM and not its filesystem one too. This way we
	// can operate on the volume using the existing storage pool functions and let the pool then handle the
	// associated filesystem volume as needed.
	poolVols, err := b.driver.ListVolumes()
	if err != nil {
		return nil, fmt.Errorf("Failed getting pool volumes: %w", err)
	}

	projectVols := make(map[string][]*backupConfig.Config)

	for _, poolVol := range poolVols {
		volType := poolVol.Type()

		// If the storage driver has returned a filesystem volume for a VM, this is a break of protocol.
		if volType == drivers.VolumeTypeVM && poolVol.ContentType() == drivers.ContentTypeFS {
			return nil, fmt.Errorf("Storage driver returned unexpected VM volume with filesystem content type (%q)", poolVol.Name())
		}

		switch volType {
		case drivers.VolumeTypeVM, drivers.VolumeTypeContainer:
			err = b.detectUnknownInstanceVolume(&poolVol, projectVols, op)
			if err != nil {
				return nil, err
			}

		case drivers.VolumeTypeCustom:
			// Get a new volume from the one returned by the storage driver.
			// This sets a new UUID for the volume that will be used later on for its database entry.
			poolVol = b.GetNewVolume(poolVol.Type(), poolVol.ContentType(), poolVol.Name(), poolVol.Config())
			err = b.detectUnknownCustomVolume(&poolVol, projectVols, op)
			if err != nil {
				return nil, err
			}

		case drivers.VolumeTypeBucket:
			// Get a new volume from the one returned by the storage driver.
			// This sets a new UUID for the volume that will be used later on for its database entry.
			poolVol = b.GetNewVolume(poolVol.Type(), poolVol.ContentType(), poolVol.Name(), poolVol.Config())
			err = b.detectUnknownBuckets(&poolVol, projectVols)
			if err != nil {
				return nil, err
			}
		}
	}

	return projectVols, nil
}

// detectUnknownInstanceVolume detects if a volume is unknown and if so attempts to mount the volume and parse the
// backup stored on it. It then runs a series of consistency checks that compare the contents of the backup file to
// the state of the volume on disk, and if all checks out, it adds the parsed backup file contents to projectVols.
func (b *lxdBackend) detectUnknownInstanceVolume(vol *drivers.Volume, projectVols map[string][]*backupConfig.Config, op *operations.Operation) error {
	volType := vol.Type()

	projectName, instName := project.InstanceParts(vol.Name())

	var instID int
	var instSnapshots []string

	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Check if an entry for the instance already exists in the DB.
		instID, err = tx.GetInstanceID(ctx, projectName, instName)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		instSnapshots, err = tx.GetInstanceSnapshotsNames(ctx, projectName, instName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Check if any entry for the instance volume already exists in the DB.
	// This will return no record for any temporary pool structs being used (as ID is -1).
	volume, err := VolumeDBGet(b, projectName, instName, volType)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	if instID > 0 && volume != nil {
		return nil // Instance record and storage record already exists in DB, no recovery needed.
	} else if instID > 0 {
		return fmt.Errorf("Instance %q in project %q already has instance DB record", instName, projectName)
	} else if volume != nil {
		return fmt.Errorf("Instance %q in project %q already has storage DB record", instName, projectName)
	}

	backupYamlPath := filepath.Join(vol.MountPath(), "backup.yaml")
	var backupConf *backupConfig.Config

	// If the instance is running, it should already be mounted, so check if the backup file
	// is already accessible, and if so parse it directly, without disturbing the mount count.
	if shared.PathExists(backupYamlPath) {
		backupConf, err = backup.ParseConfigYamlFile(backupYamlPath)
		if err != nil {
			return fmt.Errorf("Failed parsing backup file %q: %w", backupYamlPath, err)
		}
	} else {
		// If backup file not accessible, we take this to mean the instance isn't running
		// and so we need to mount the volume to access the backup file and then unmount.
		// This will also create the mount path if needed.
		err = vol.MountTask(func(_ string, _ *operations.Operation) error {
			backupConf, err = backup.ParseConfigYamlFile(backupYamlPath)
			if err != nil {
				return fmt.Errorf("Failed parsing backup file %q: %w", backupYamlPath, err)
			}

			return nil
		}, op)
		if err != nil {
			return err
		}
	}

	// Run some consistency checks on the backup file contents.
	if backupConf.Pool != nil {
		if backupConf.Pool.Name != b.name {
			return fmt.Errorf("Instance %q in project %q has pool name mismatch in its backup file (%q doesn't match's pool's %q)", instName, projectName, backupConf.Pool.Name, b.name)
		}

		if backupConf.Pool.Driver != b.Driver().Info().Name {
			return fmt.Errorf("Instance %q in project %q has pool driver mismatch in its backup file (%q doesn't match's pool's %q)", instName, projectName, backupConf.Pool.Driver, b.Driver().Name())
		}
	}

	if backupConf.Container == nil {
		return fmt.Errorf("Instance %q in project %q has no instance information in its backup file", instName, projectName)
	}

	if instName != backupConf.Container.Name {
		return fmt.Errorf("Instance %q in project %q has a different instance name in its backup file (%q)", instName, projectName, backupConf.Container.Name)
	}

	apiInstType, err := VolumeTypeToAPIInstanceType(volType)
	if err != nil {
		return fmt.Errorf("Failed checking instance type for instance %q in project %q: %w", instName, projectName, err)
	}

	if apiInstType != api.InstanceType(backupConf.Container.Type) {
		return fmt.Errorf("Instance %q in project %q has a different instance type in its backup file (%q)", instName, projectName, backupConf.Container.Type)
	}

	if backupConf.Volume == nil {
		return fmt.Errorf("Instance %q in project %q has no volume information in its backup file", instName, projectName)
	}

	if instName != backupConf.Volume.Name {
		return fmt.Errorf("Instance %q in project %q has a different volume name in its backup file (%q)", instName, projectName, backupConf.Volume.Name)
	}

	instVolDBType, err := cluster.StoragePoolVolumeTypeFromName(backupConf.Volume.Type)
	if err != nil {
		return fmt.Errorf("Failed checking instance volume type for instance %q in project %q: %w", instName, projectName, err)
	}

	instVolType := VolumeDBTypeToType(instVolDBType)

	if volType != instVolType {
		return fmt.Errorf("Instance %q in project %q has a different volume type in its backup file (%q)", instName, projectName, backupConf.Volume.Type)
	}

	// Add to volume to unknown volumes list for the project.
	if projectVols[projectName] == nil {
		projectVols[projectName] = []*backupConfig.Config{backupConf}
	} else {
		projectVols[projectName] = append(projectVols[projectName], backupConf)
	}

	// Check snapshots are consistent between storage layer and backup config file.
	_, err = b.CheckInstanceBackupFileSnapshots(backupConf, projectName, nil)
	if err != nil {
		return fmt.Errorf("Instance %q in project %q has snapshot inconsistency: %w", instName, projectName, err)
	}

	// Check there are no existing DB records present for snapshots.
	for _, snapshot := range backupConf.Snapshots {
		fullSnapshotName := drivers.GetSnapshotVolumeName(instName, snapshot.Name)

		// Check if an entry for the instance already exists in the DB.
		if shared.ValueInSlice(fullSnapshotName, instSnapshots) {
			return fmt.Errorf("Instance %q snapshot %q in project %q already has instance DB record", instName, snapshot.Name, projectName)
		}

		// Check if any entry for the instance snapshot volume already exists in the DB.
		// This will return no record for any temporary pool structs being used (as ID is -1).
		volume, err := VolumeDBGet(b, projectName, fullSnapshotName, volType)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		} else if volume != nil {
			return fmt.Errorf("Instance %q snapshot %q in project %q already has storage DB record", instName, snapshot.Name, projectName)
		}
	}

	return nil
}

// detectUnknownCustomVolume detects if a volume is unknown and if so attempts to discover the filesystem of the
// volume (for filesystem volumes). It then runs a series of consistency checks, and if all checks out, it adds
// generates a simulated backup config for the custom volume and adds it to projectVols.
func (b *lxdBackend) detectUnknownCustomVolume(vol *drivers.Volume, projectVols map[string][]*backupConfig.Config, op *operations.Operation) error {
	volType := vol.Type()

	projectName, volName := project.StorageVolumeParts(vol.Name())

	// Check if any entry for the custom volume already exists in the DB.
	// This will return no record for any temporary pool structs being used (as ID is -1).
	volume, err := VolumeDBGet(b, projectName, volName, volType)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	} else if volume != nil {
		return nil // Storage record already exists in DB, no recovery needed.
	}

	// Get a list of snapshots that exist on storage device.
	snapshots, err := b.driver.VolumeSnapshots(*vol, op)
	if err != nil {
		return err
	}

	contentType := vol.ContentType()
	var apiContentType string

	switch contentType {
	case drivers.ContentTypeBlock:
		apiContentType = cluster.StoragePoolVolumeContentTypeNameBlock
	case drivers.ContentTypeISO:
		apiContentType = cluster.StoragePoolVolumeContentTypeNameISO
	case drivers.ContentTypeFS:
		apiContentType = cluster.StoragePoolVolumeContentTypeNameFS

		// Detect block volume filesystem (by mounting it (if not already) with filesystem probe mode).
		if vol.IsBlockBacked() {
			var blockFS string
			mountPath := vol.MountPath()
			if filesystem.IsMountPoint(mountPath) {
				blockFS, err = filesystem.Detect(mountPath)
				if err != nil {
					return err
				}
			} else {
				err = vol.MountTask(func(mountPath string, _ *operations.Operation) error {
					blockFS, err = filesystem.Detect(mountPath)
					if err != nil {
						return err
					}

					return nil
				}, op)
				if err != nil {
					return err
				}
			}

			// Record detected filesystem in config.
			vol.Config()["block.filesystem"] = blockFS
		}

	default:
		return fmt.Errorf("Unknown custom volume content type %q", contentType)
	}

	// This may not always be the correct thing to do, but seeing as we don't know what the volume's config
	// was lets take a best guess that it was the default config.
	err = b.driver.FillVolumeConfig(*vol)
	if err != nil {
		return fmt.Errorf("Failed filling custom volume default config: %w", err)
	}

	// Check the filesystem detected is valid for the storage driver.
	err = b.driver.ValidateVolume(*vol, false)
	if err != nil {
		return fmt.Errorf("Failed custom volume validation: %w", err)
	}

	backupConf := &backupConfig.Config{
		Volume: &api.StorageVolume{
			Name:        volName,
			Type:        cluster.StoragePoolVolumeTypeNameCustom,
			ContentType: apiContentType,
			Config:      vol.Config(),
		},
	}

	// Populate snaphot volumes.
	for _, snapOnlyName := range snapshots {
		snapFullName := drivers.GetSnapshotVolumeName(volName, snapOnlyName)

		// Have to assume the snapshot volume config is same as parent.
		snapVol := b.GetNewVolume(volType, contentType, snapFullName, vol.Config())

		backupConf.VolumeSnapshots = append(backupConf.VolumeSnapshots, &api.StorageVolumeSnapshot{
			Name:        snapOnlyName, // Snapshot only name, not full name.
			Config:      snapVol.Config(),
			ContentType: apiContentType,
		})
	}

	// Add to volume to unknown volumes list for the project.
	if projectVols[projectName] == nil {
		projectVols[projectName] = []*backupConfig.Config{backupConf}
	} else {
		projectVols[projectName] = append(projectVols[projectName], backupConf)
	}

	return nil
}

// detectUnknownBuckets detects if a bucket is unknown and if so attempts to discover the filesystem of the
// bucket. It then runs a series of consistency checks, and if all checks out, it generates a simulated backup
// config for the bucket and adds it to projectVols.
func (b *lxdBackend) detectUnknownBuckets(vol *drivers.Volume, projectVols map[string][]*backupConfig.Config) error {
	projectName, bucketName := project.StorageVolumeParts(vol.Name())

	// Check if any entry for the bucket already exists in the DB.
	bucket, err := BucketDBGet(b, projectName, bucketName, true)
	if err != nil && !response.IsNotFoundError(err) {
		return err
	} else if bucket != nil {
		return nil // Storage record already exists in DB, no recovery needed.
	}

	// This may not always be the correct thing to do, but seeing as we don't know what the volume's config
	// was lets take a best guess that it was the default config.
	err = b.driver.FillVolumeConfig(*vol)
	if err != nil {
		return fmt.Errorf("Failed filling bucket default config: %w", err)
	}

	// Check the detected filesystem is valid for the storage driver.
	err = b.driver.ValidateVolume(*vol, false)
	if err != nil {
		return fmt.Errorf("Failed bucket validation: %w", err)
	}

	backupConf := &backupConfig.Config{
		Bucket: &api.StorageBucket{
			Name:   bucketName,
			Config: vol.Config(),
		},
	}

	// Add the bucket to unknown volumes list for the project.
	if projectVols[projectName] == nil {
		projectVols[projectName] = []*backupConfig.Config{backupConf}
	} else {
		projectVols[projectName] = append(projectVols[projectName], backupConf)
	}

	return nil
}

// ImportInstance takes an existing instance volume on the storage backend and ensures that the volume directories
// and symlinks are restored as needed to make it operational with LXD. Used during the recovery import stage.
// If the instance exists on the local cluster member then the local mount status is restored as needed.
// If the optional poolVol argument is provided then it is used to create the storage volume database records.
func (b *lxdBackend) ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	l := b.logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	l.Debug("ImportInstance started")
	defer l.Debug("ImportInstance finished")

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	var snapshots []string

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get any snapshots the instance has in the format <instance name>/<snapshot name>.
		snapshots, err = tx.GetInstanceSnapshotsNames(ctx, inst.Project().Name, inst.Name())

		return err
	})
	if err != nil {
		return nil, err
	}

	contentType := InstanceContentType(inst)

	revert := revert.New()
	defer revert.Fail()

	var volumeConfig map[string]string

	if poolVol != nil && poolVol.Volume != nil {
		// Use volume config from backup file config if present.
		volumeConfig = poolVol.Volume.Config
	}

	// Generate the effective root device volume for instance.
	volStorageName := project.Instance(inst.Project().Name, inst.Name())

	// Copy the volume's config so VolumeDBCreate can safely modify the copy if needed.
	vol := b.GetNewVolume(volType, contentType, volStorageName, volumeConfig)

	// Create storage volume database records if in recover mode.
	if poolVol != nil {
		creationDate := inst.CreationDate()

		if poolVol.Volume != nil {
			if !poolVol.Volume.CreatedAt.IsZero() {
				creationDate = poolVol.Volume.CreatedAt
			}
		}

		// Validate config and create database entry for recovered storage volume.
		err = VolumeDBCreate(b, inst.Project().Name, inst.Name(), "", volType, false, vol.Config(), creationDate, time.Time{}, contentType, false, true)
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, inst.Name(), volType) })

		if len(snapshots) > 0 && len(poolVol.VolumeSnapshots) > 0 {
			// Create storage volume snapshot DB records from the entries in the backup file config.
			for _, poolVolSnap := range poolVol.VolumeSnapshots {
				fullSnapName := drivers.GetSnapshotVolumeName(inst.Name(), poolVolSnap.Name)

				// Copy volume config from backup file if present,
				// so VolumeDBCreate can safely modify the copy if needed.
				snapVol := b.GetNewVolume(volType, contentType, fullSnapName, poolVolSnap.Config)

				// Validate config and create database entry for recovered storage volume.
				err = VolumeDBCreate(b, inst.Project().Name, fullSnapName, poolVolSnap.Description, volType, true, snapVol.Config(), poolVolSnap.CreatedAt, time.Time{}, contentType, false, true)
				if err != nil {
					return nil, err
				}

				revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, fullSnapName, volType) })
			}
		} else {
			b.logger.Warn("Missing volume snapshot info in backup config, using parent volume config")

			// Create storage volume snapshot DB records based on instance snapshot list, as the
			// backup config doesn't contain the required info. This is needed because there was a
			// historical bug that meant that the instance's backup file didn't store the storage
			// volume snapshot info.
			for _, i := range snapshots {
				fullSnapName := i // Local var for revert.

				// Copy the parent volume's config,
				// so VolumeDBCreate can safely modify the copy if needed.
				snapVol := b.GetNewVolume(volType, contentType, fullSnapName, volumeConfig)

				// Validate config and create database entry for new storage volume.
				err = VolumeDBCreate(b, inst.Project().Name, fullSnapName, "", volType, true, snapVol.Config(), time.Time{}, time.Time{}, contentType, false, true)
				if err != nil {
					return nil, err
				}

				revert.Add(func() { _ = VolumeDBDelete(b, inst.Project().Name, fullSnapName, volType) })
			}
		}
	}

	err = b.applyInstanceRootDiskOverrides(inst, &vol)
	if err != nil {
		return nil, err
	}

	err = vol.EnsureMountPath()
	if err != nil {
		return nil, err
	}

	// Only attempt to restore mount status on instance's local cluster member.
	if inst.Location() == b.state.ServerName {
		l.Debug("Restoring local instance mount status")

		if inst.IsRunning() {
			// If the instance is running then this implies the volume is mounted, but if the LXD
			// daemon has been restarted since the DB records were removed then there will be no mount
			// reference counter showing the volume is in use. If this is the case then call mount the
			// volume to increment the reference counter.
			if !vol.MountInUse() {
				_, err = b.MountInstance(inst, op)
				if err != nil {
					return nil, fmt.Errorf("Failed mounting instance: %w", err)
				}
			}
		} else {
			// If the instance isn't running then try and unmount it to ensure consistent state after
			// import.
			err = b.UnmountInstance(inst, op)
			if err != nil {
				return nil, fmt.Errorf("Failed unmounting instance: %w", err)
			}
		}
	}

	// Create symlink.
	err = b.ensureInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name(), vol.MountPath())
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		// Remove symlinks.
		_ = b.removeInstanceSymlink(inst.Type(), inst.Project().Name, inst.Name())
		_ = b.removeInstanceSnapshotSymlinkIfUnused(inst.Type(), inst.Project().Name, inst.Name())
	})

	// Create snapshot mount paths and snapshot symlink if needed.
	if len(snapshots) > 0 {
		for _, snapName := range snapshots {
			_, snapOnlyName, _ := api.GetParentAndSnapshotName(snapName)
			l.Debug("Ensuring instance snapshot mount path", logger.Ctx{"snapshot": snapOnlyName})

			snapVol, err := vol.NewSnapshot(snapOnlyName)
			if err != nil {
				return nil, err
			}

			err = snapVol.EnsureMountPath()
			if err != nil {
				return nil, err
			}
		}

		err = b.ensureInstanceSnapshotSymlink(inst.Type(), inst.Project().Name, inst.Name())
		if err != nil {
			return nil, err
		}
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, err
}

// BackupCustomVolume creates a backup of an existing custom volume.
func (b *lxdBackend) BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volume": volName, "optimized": optimized, "snapshots": snapshots})
	l.Debug("BackupCustomVolume started")
	defer l.Debug("BackupCustomVolume finished")

	volume, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
	if err != nil {
		return err
	}

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volume.Name)

	contentDBType, err := cluster.StoragePoolVolumeContentTypeFromName(volume.ContentType)
	if err != nil {
		return err
	}

	contentType := VolumeDBContentTypeToContentType(contentDBType)

	if contentType != drivers.ContentTypeFS && contentType != drivers.ContentTypeBlock {
		return fmt.Errorf("Volume of content type %q cannot be backed up", contentType)
	}

	var snapNames []string
	var sourceSnapshots []drivers.Volume
	if snapshots {
		// Get snapshots in age order, oldest first, and pass names to storage driver.
		volSnaps, err := VolumeDBSnapshotsGet(b, projectName, volName, drivers.VolumeTypeCustom)
		if err != nil {
			return err
		}

		snapNames = make([]string, 0, len(volSnaps))
		sourceSnapshots = make([]drivers.Volume, 0, len(volSnaps))
		for _, volSnap := range volSnaps {
			_, snapName, _ := api.GetParentAndSnapshotName(volSnap.Name)
			snapNames = append(snapNames, snapName)

			snapshotStorageName := project.StorageVolume(projectName, volSnap.Name)
			sourceSnapshots = append(sourceSnapshots, b.GetVolume(drivers.VolumeTypeCustom, contentType, snapshotStorageName, volSnap.Config))
		}
	}

	vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentType(volume.ContentType), volStorageName, volume.Config)

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	err = b.driver.BackupVolume(volCopy, tarWriter, optimized, snapNames, op)
	if err != nil {
		return err
	}

	return nil
}

// CreateCustomVolumeFromISO creates a custom volume from the given ISO source data.
func (b *lxdBackend) CreateCustomVolumeFromISO(projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volume": volName})
	l.Debug("CreateCustomVolumeFromISO started")
	defer l.Debug("CreateCustomVolumeFromISO finished")

	// Check whether we are allowed to create volumes.
	req := api.StorageVolumesPost{
		Name: volName,
		StorageVolumePut: api.StorageVolumePut{
			Config: map[string]string{
				"size": strconv.FormatInt(size, 10),
			},
		},
	}

	err := b.state.DB.Cluster.Transaction(b.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return limits.AllowVolumeCreation(ctx, b.state.GlobalConfig, tx, projectName, b.name, req)
	})
	if err != nil {
		return fmt.Errorf("Failed checking volume creation allowed: %w", err)
	}

	revert := revert.New()
	defer revert.Fail()

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(projectName, volName)

	vol := b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentTypeISO, volStorageName, req.Config)

	volExists, err := b.driver.HasVolume(vol)
	if err != nil {
		return err
	}

	if volExists {
		return fmt.Errorf("Cannot create volume, already exists on target storage")
	}

	// Validate config and create database entry for new storage volume.
	err = VolumeDBCreate(b, projectName, volName, "", vol.Type(), false, vol.Config(), time.Now(), time.Time{}, vol.ContentType(), true, true)
	if err != nil {
		return fmt.Errorf("Failed creating database entry for custom volume: %w", err)
	}

	revert.Add(func() { _ = VolumeDBDelete(b, projectName, volName, vol.Type()) })

	_, err = srcData.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}

	volFiller := drivers.VolumeFiller{
		Fill: b.isoFiller(srcData),
	}

	// Unpack the ISO into the new storage volume(s).
	err = b.driver.CreateVolume(vol, &volFiller, op)
	if err != nil {
		return fmt.Errorf("Failed creating volume: %w", err)
	}

	eventCtx := logger.Ctx{"type": vol.Type()}
	if !b.Driver().Info().Remote {
		eventCtx["location"] = b.state.ServerName
	}

	b.state.Events.SendLifecycle(projectName, lifecycle.StorageVolumeCreated.Event(vol, string(vol.Type()), projectName, op, eventCtx))

	revert.Success()
	return nil
}

// CreateCustomVolumeFromBackup creates a custom volume from the given backup info.
func (b *lxdBackend) CreateCustomVolumeFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error {
	l := b.logger.AddContext(logger.Ctx{"project": srcBackup.Project, "volume": srcBackup.Name, "snapshots": srcBackup.Snapshots, "optimizedStorage": *srcBackup.OptimizedStorage})
	l.Debug("CreateCustomVolumeFromBackup started")
	defer l.Debug("CreateCustomVolumeFromBackup finished")

	if srcBackup.Config == nil || srcBackup.Config.Volume == nil {
		return fmt.Errorf("Valid volume config not found in index")
	}

	if len(srcBackup.Snapshots) != len(srcBackup.Config.VolumeSnapshots) {
		return fmt.Errorf("Valid volume snapshot config not found in index")
	}

	// Validate the names in the index.yaml file as these could be malicious.
	err := ValidVolumeName(srcBackup.Name)
	if err != nil {
		return err
	}

	err = ValidVolumeName(srcBackup.Config.Volume.Name)
	if err != nil {
		return err
	}

	for _, snapName := range srcBackup.Snapshots {
		err = ValidVolumeName(snapName)
		if err != nil {
			return err
		}
	}

	for _, snap := range srcBackup.Config.VolumeSnapshots {
		err = ValidVolumeName(snap.Name)
		if err != nil {
			return err
		}
	}

	// Check whether we are allowed to create volumes.
	req := api.StorageVolumesPost{
		StorageVolumePut: api.StorageVolumePut{
			Config: srcBackup.Config.Volume.Config,
		},
		Name: srcBackup.Name,
	}

	err = b.state.DB.Cluster.Transaction(b.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return limits.AllowVolumeCreation(ctx, b.state.GlobalConfig, tx, srcBackup.Project, b.name, req)
	})
	if err != nil {
		return fmt.Errorf("Failed checking volume creation allowed: %w", err)
	}

	revert := revert.New()
	defer revert.Fail()

	// Get the volume name on storage.
	volStorageName := project.StorageVolume(srcBackup.Project, srcBackup.Name)

	vol := b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentType(srcBackup.Config.Volume.ContentType), volStorageName, srcBackup.Config.Volume.Config)

	// Validate config and create database entry for new storage volume.
	// Strip unsupported config keys (in case the export was made from a different type of storage pool).
	err = VolumeDBCreate(b, srcBackup.Project, srcBackup.Name, srcBackup.Config.Volume.Description, vol.Type(), false, vol.Config(), srcBackup.Config.Volume.CreatedAt, time.Time{}, vol.ContentType(), true, true)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = VolumeDBDelete(b, srcBackup.Project, srcBackup.Name, vol.Type()) })

	sourceSnapshots := make([]drivers.Volume, 0, len(srcBackup.Config.VolumeSnapshots))

	// Create database entries fro new storage volume snapshots.
	for _, s := range srcBackup.Config.VolumeSnapshots {
		snapshot := s // Local var for revert.
		snapName := snapshot.Name

		// Due to a historical bug, the volume snapshot names were sometimes written in their full form
		// (<parent>/<snap>) rather than the expected snapshot name only form, so we need to handle both.
		if shared.IsSnapshot(snapshot.Name) {
			_, snapName, _ = api.GetParentAndSnapshotName(snapshot.Name)
		}

		fullSnapName := drivers.GetSnapshotVolumeName(srcBackup.Name, snapName)
		snapVolStorageName := project.StorageVolume(srcBackup.Project, fullSnapName)
		snapVol := b.GetNewVolume(drivers.VolumeTypeCustom, drivers.ContentType(srcBackup.Config.Volume.ContentType), snapVolStorageName, snapshot.Config)

		// Validate config and create database entry for new storage volume.
		// Strip unsupported config keys (in case the export was made from a different type of storage pool).
		err = VolumeDBCreate(b, srcBackup.Project, fullSnapName, snapshot.Description, snapVol.Type(), true, snapVol.Config(), snapshot.CreatedAt, *snapshot.ExpiresAt, snapVol.ContentType(), true, true)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = VolumeDBDelete(b, srcBackup.Project, fullSnapName, snapVol.Type()) })

		sourceSnapshots = append(sourceSnapshots, snapVol)
	}

	volCopy := drivers.NewVolumeCopy(vol, sourceSnapshots...)

	// Unpack the backup into the new storage volume(s).
	volPostHook, revertHook, err := b.driver.CreateVolumeFromBackup(volCopy, srcBackup, srcData, op)
	if err != nil {
		return err
	}

	if revertHook != nil {
		revert.Add(revertHook)
	}

	// If the driver returned a post hook, return error as custom volumes don't need post hooks and we expect
	// the storage driver to understand this distinction and ensure that all activities done in the postHook
	// normally are done in CreateVolumeFromBackup as the DB record is created ahead of time.
	if volPostHook != nil {
		return fmt.Errorf("Custom volume restore doesn't support post hooks")
	}

	eventCtx := logger.Ctx{"type": vol.Type()}
	if !b.Driver().Info().Remote {
		eventCtx["location"] = b.state.ServerName
	}

	b.state.Events.SendLifecycle(srcBackup.Project, lifecycle.StorageVolumeCreated.Event(vol, string(vol.Type()), srcBackup.Project, op, eventCtx))

	revert.Success()
	return nil
}

// getParentVolumeUUID returns the UUID of the parent's volume.
// If the volume has no parent, an empty string is returned.
func (b *lxdBackend) getParentVolumeUUID(vol drivers.Volume, projectName string) (string, error) {
	parentName, _, isSnapshot := api.GetParentAndSnapshotName(vol.Name())
	if !isSnapshot {
		// Volume has no parent.
		return "", nil
	}

	// Ensure the parent name does not contain a project prefix.
	_, parentName = project.StorageVolumeParts(parentName)

	// Load storage volume from the database.
	parentDBVol, err := VolumeDBGet(b, projectName, parentName, vol.Type())
	if err != nil {
		return "", fmt.Errorf("Failed to extract parent UUID from snapshot %q in project %q: %w", vol.Name(), projectName, err)
	}

	// Extract parent volume UUID.
	parentUUID := parentDBVol.Config["volatile.uuid"]
	if parentUUID == "" {
		return "", fmt.Errorf("Parent volume %q of snapshot %q in project %q does not have UUID set)", parentName, projectName, vol.Name())
	}

	return parentUUID, nil
}
