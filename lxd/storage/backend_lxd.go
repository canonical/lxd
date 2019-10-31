package storage

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/storage/memorypipe"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

type lxdBackend struct {
	driver drivers.Driver
	id     int64
	name   string
	state  *state.State
	logger logger.Logger
}

func (b *lxdBackend) DaemonState() *state.State {
	return b.state
}

// ID returns the storage pool ID.
func (b *lxdBackend) ID() int64 {
	return b.id
}

// Name returns the storage pool name.
func (b *lxdBackend) Name() string {
	return b.name
}

// Driver returns the storage pool driver.
func (b *lxdBackend) Driver() drivers.Driver {
	return b.driver
}

// MigrationTypes returns the migration transport method preferred when sending a migration,
// based on the migration method requested by the driver's ability.
func (b *lxdBackend) MigrationTypes(contentType drivers.ContentType) []migration.Type {
	return b.driver.MigrationTypes(contentType)
}

// create creates the storage pool layout on the storage device.
func (b *lxdBackend) create(dbPool *api.StoragePool, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"args": dbPool})
	logger.Debug("create started")
	defer logger.Debug("created finished")

	revertPath := true

	// Create the storage path.
	path := drivers.GetPoolMountPath(b.name)
	err := os.MkdirAll(path, 0711)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Undo the storage path create if there is an error.
	defer func() {
		if !revertPath {
			return
		}

		os.RemoveAll(path)
	}()

	// Create the storage pool on the storage device.
	err = b.driver.Create()
	if err != nil {
		return err
	}

	// Mount the storage pool.
	ourMount, err := b.driver.Mount()
	if err != nil {
		return err
	}

	// We expect the caller of create to mount the pool if needed, so we should unmount after
	// storage struct has been created.
	if ourMount {
		defer b.driver.Unmount()
	}

	// Create the directory structure.
	err = createStorageStructure(path)
	if err != nil {
		return err
	}

	revertPath = false
	return nil
}

// newVolume returns a new Volume instance.
func (b *lxdBackend) newVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	return drivers.NewVolume(b.driver, b.name, volType, contentType, volName, volConfig)
}

func (b *lxdBackend) GetResources() (*api.ResourcesStoragePool, error) {
	logger := logging.AddContext(b.logger, nil)
	logger.Debug("GetResources started")
	defer logger.Debug("GetResources finished")

	return b.driver.GetResources()
}

// Delete removes the pool.
func (b *lxdBackend) Delete(op *operations.Operation) error {
	logger := logging.AddContext(b.logger, nil)
	logger.Debug("Delete started")
	defer logger.Debug("Delete finished")

	// Delete the low-level storage.
	err := b.driver.Delete(op)
	if err != nil {
		return err
	}

	// Delete the mountpoint.
	path := shared.VarPath("storage-pools", b.name)
	err = os.Remove(path)
	if err != nil {
		return err
	}

	return nil
}

// Mount mounts the storage pool.
func (b *lxdBackend) Mount() (bool, error) {
	logger := logging.AddContext(b.logger, nil)
	logger.Debug("Mount started")
	defer logger.Debug("Mount finished")

	return b.driver.Mount()
}

// Unmount unmounts the storage pool.
func (b *lxdBackend) Unmount() (bool, error) {
	logger := logging.AddContext(b.logger, nil)
	logger.Debug("Unmount started")
	defer logger.Debug("Unmount finished")

	return b.driver.Unmount()
}

func (b *lxdBackend) CreateInstance(inst Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromBackup(inst Instance, sourcePath string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromCopy(inst Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromImage(inst Instance, fingerprint string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromMigration(inst Instance, conn io.ReadWriteCloser, args migration.SinkArgs, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameInstance(inst Instance, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteInstance(inst Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MigrateInstance(inst Instance, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, ErrNotImplemented
}

func (b *lxdBackend) RefreshInstance(inst Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) BackupInstance(inst Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) GetInstanceUsage(i Instance) (uint64, error) {
	return 0, ErrNotImplemented
}

func (b *lxdBackend) SetInstanceQuota(inst Instance, quota uint64) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MountInstance(inst Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) UnmountInstance(inst Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) GetInstanceDisk(inst Instance) (string, string, error) {
	return "", "", ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceSnapshot(inst Instance, name string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameInstanceSnapshot(inst Instance, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteInstanceSnapshot(inst Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RestoreInstanceSnapshot(inst Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MountInstanceSnapshot(inst Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) UnmountInstanceSnapshot(inst Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) CreateImage(img api.Image, op *operations.Operation) error {
	return ErrNotImplemented
}

// DeleteImage removes an image from the database and underlying storage device if needed.
func (b *lxdBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"fingerprint": fingerprint})
	logger.Debug("DeleteImage started")
	defer logger.Debug("DeleteImage finished")

	regexSHA256, err := regexp.Compile("^[0-9a-f]{64}$")
	if err != nil {
		return err
	}

	if !regexSHA256.MatchString(fingerprint) {
		return fmt.Errorf("Invalid fingerprint")
	}

	err = b.driver.DeleteVolume(drivers.VolumeTypeImage, fingerprint, op)
	if err != nil {
		return nil
	}

	err = b.state.Cluster.StoragePoolVolumeDelete("default", fingerprint, db.StoragePoolVolumeTypeImage, b.ID())
	if err != nil {
		return err
	}

	return ErrNotImplemented
}

// CreateCustomVolume creates an empty custom volume.
func (b *lxdBackend) CreateCustomVolume(volName, desc string, config map[string]string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "desc": desc, "config": config})
	logger.Debug("CreateCustomVolume started")
	defer logger.Debug("CreateCustomVolume finished")

	// Validate config.
	err := b.driver.ValidateVolume(b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, volName, config), false)
	if err != nil {
		return err
	}

	// Create database entry for new storage volume.
	err = VolumeDBCreate(b.state, b.name, volName, desc, db.StoragePoolVolumeTypeNameCustom, false, config)
	if err != nil {
		return err
	}

	revertDB := true
	defer func() {
		if revertDB {
			b.state.Cluster.StoragePoolVolumeDelete("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
		}
	}()

	// Create the empty custom volume on the storage device.
	newVol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, volName, config)
	err = b.driver.CreateVolume(newVol, nil, op)
	if err != nil {
		return err
	}

	revertDB = false
	return nil
}

// CreateCustomVolumeFromCopy creates a custom volume from an existing custom volume.
// It copies the snapshots from the source volume by default, but can be disabled if requested.
func (b *lxdBackend) CreateCustomVolumeFromCopy(volName, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "desc": desc, "config": config, "srcPoolName": srcPoolName, "srcVolName": srcVolName, "srcVolOnly": srcVolOnly})
	logger.Debug("CreateCustomVolumeFromCopy started")
	defer logger.Debug("CreateCustomVolumeFromCopy finished")

	// Setup the source pool backend instance.
	var srcPool *lxdBackend
	if b.name == srcPoolName {
		srcPool = b // Source and target are in the same pool so share pool var.
	} else {
		// Source is in a different pool to target, so load the pool.
		tmpPool, err := GetPoolByName(b.state, srcPoolName)
		if err != nil {
			return err
		}

		// Convert to lxdBackend so we can access driver.
		tmpBackend, ok := tmpPool.(*lxdBackend)
		if !ok {
			return fmt.Errorf("Pool is not an lxdBackend")
		}

		srcPool = tmpBackend
	}

	// Check source volume exists and is custom type.
	_, srcVolRow, err := b.state.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", srcVolName, db.StoragePoolVolumeTypeCustom, srcPool.ID())
	if err != nil {
		if err == db.ErrNoSuchObject {
			return fmt.Errorf("Source volume doesn't exist")
		}

		return err
	}

	// Use the source volume's config if not supplied.
	if config == nil {
		config = srcVolRow.Config
	}

	// Use the source volume's description if not supplied.
	if desc == "" {
		desc = srcVolRow.Description
	}

	// If we are copying snapshots, retrieve a list of snapshots from source volume.
	snapshotNames := []string{}
	if !srcVolOnly {
		snapshots, err := VolumeSnapshotsGet(b.state, srcPoolName, srcVolName, db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		for _, snapshot := range snapshots {
			_, snapShotName, _ := shared.ContainerGetParentAndSnapshotName(snapshot.Name)
			snapshotNames = append(snapshotNames, snapShotName)
		}
	}

	// Create in-memory pipe pair to simulate a connection between the sender and receiver.
	aEnd, bEnd := memorypipe.NewPipePair()

	// Negotiate the migration type to use.
	offeredTypes := srcPool.MigrationTypes(drivers.ContentTypeFS)
	offerHeader := migration.TypesToHeader(offeredTypes...)
	migrationType, err := migration.MatchTypes(offerHeader, migration.MigrationFSType_RSYNC, b.MigrationTypes(drivers.ContentTypeFS))
	if err != nil {
		return fmt.Errorf("Failed to neogotiate copy migration type: %v", err)
	}

	// Run sender and receiver in separate go routines to prevent deadlocks.
	aEndErrCh := make(chan error, 1)
	bEndErrCh := make(chan error, 1)
	go func() {
		err := srcPool.MigrateCustomVolume(aEnd, migration.VolumeSourceArgs{
			Name:          srcVolName,
			Snapshots:     snapshotNames,
			MigrationType: migrationType,
			TrackProgress: true, // Do use a progress tracker on sender.
		}, op)

		aEndErrCh <- err
	}()

	go func() {
		err := b.CreateCustomVolumeFromMigration(bEnd, migration.VolumeTargetArgs{
			Name:          volName,
			Description:   desc,
			Config:        config,
			Snapshots:     snapshotNames,
			MigrationType: migrationType,
			TrackProgress: false, // Do not a progress tracker on receiver.

		}, op)

		bEndErrCh <- err
	}()

	// Capture errors from the sender and receiver from their result channels.
	errs := []error{}
	aEndErr := <-aEndErrCh
	if aEndErr != nil {
		errs = append(errs, aEndErr)
	}

	bEndErr := <-bEndErrCh
	if bEndErr != nil {
		errs = append(errs, bEndErr)
	}

	if len(errs) > 0 {
		return fmt.Errorf("Create custom volume from copy failed: %v", errs)
	}

	return nil
}

// MigrateCustomVolume sends a volume for migration.
func (b *lxdBackend) MigrateCustomVolume(conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": args.Name, "args": args})
	logger.Debug("MigrateCustomVolume started")
	defer logger.Debug("MigrateCustomVolume finished")

	vol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, args.Name, nil)
	err := b.driver.MigrateVolume(vol, conn, args, op)
	if err != nil {
		return err
	}

	return nil
}

// CreateCustomVolumeFromMigration receives a volume being migrated.
func (b *lxdBackend) CreateCustomVolumeFromMigration(conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": args.Name, "args": args})
	logger.Debug("CreateCustomVolumeFromMigration started")
	defer logger.Debug("CreateCustomVolumeFromMigration finished")

	// Create slice to record DB volumes created if revert needed later.
	revertDBVolumes := []string{}
	defer func() {
		// Remove any DB volume rows created if we are reverting.
		for _, volName := range revertDBVolumes {
			b.state.Cluster.StoragePoolVolumeDelete("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
		}
	}()

	// Check the supplied config and remove any fields not relevant for destination pool type.
	err := b.driver.ValidateVolume(b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, args.Name, args.Config), true)
	if err != nil {
		return err
	}

	// Create database entry for new storage volume.
	err = VolumeDBCreate(b.state, b.name, args.Name, args.Description, db.StoragePoolVolumeTypeNameCustom, false, args.Config)
	if err != nil {
		return err
	}

	revertDBVolumes = append(revertDBVolumes, args.Name)

	if len(args.Snapshots) > 0 {
		for _, snapName := range args.Snapshots {
			newSnapshotName := drivers.GetSnapshotVolumeName(args.Name, snapName)

			// Create database entry for new storage volume snapshot.
			err = VolumeDBCreate(b.state, b.name, newSnapshotName, args.Description, db.StoragePoolVolumeTypeNameCustom, true, args.Config)
			if err != nil {
				return err
			}

			revertDBVolumes = append(revertDBVolumes, newSnapshotName)
		}
	}

	vol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, args.Name, args.Config)
	err = b.driver.CreateVolumeFromMigration(vol, conn, args, op)
	if err != nil {
		return nil
	}

	revertDBVolumes = nil
	return nil
}

// RenameCustomVolume renames a custom volume and its snapshots.
func (b *lxdBackend) RenameCustomVolume(volName string, newVolName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "newVolName": newVolName})
	logger.Debug("RenameCustomVolume started")
	defer logger.Debug("RenameCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	if shared.IsSnapshot(newVolName) {
		return fmt.Errorf("New volume name cannot be a snapshot")
	}

	type volRevert struct {
		oldName string
		newName string
	}

	// Create slice to record DB volumes renamed if revert needed later.
	revertDBVolumes := []volRevert{}
	defer func() {
		// Remove any DB volume rows created if we are reverting.
		for _, vol := range revertDBVolumes {
			b.state.Cluster.StoragePoolVolumeRename("default", vol.newName, vol.oldName, db.StoragePoolVolumeTypeCustom, b.ID())
		}
	}()

	// Rename each snapshot to have the new parent volume prefix.
	snapshots, err := VolumeSnapshotsGet(b.state, b.name, volName, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return err
	}

	for _, srcSnapshot := range snapshots {
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(srcSnapshot.Name)
		newSnapVolName := drivers.GetSnapshotVolumeName(newVolName, snapName)
		err = b.state.Cluster.StoragePoolVolumeRename("default", srcSnapshot.Name, newSnapVolName, db.StoragePoolVolumeTypeCustom, b.ID())
		if err != nil {
			return err
		}

		revertDBVolumes = append(revertDBVolumes, volRevert{
			newName: newSnapVolName,
			oldName: srcSnapshot.Name,
		})
	}

	err = b.state.Cluster.StoragePoolVolumeRename("default", volName, newVolName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		return err
	}

	revertDBVolumes = append(revertDBVolumes, volRevert{
		newName: newVolName,
		oldName: volName,
	})

	err = b.driver.RenameVolume(drivers.VolumeTypeCustom, volName, newVolName, op)
	if err != nil {
		return err
	}

	revertDBVolumes = nil
	return nil
}

// UpdateCustomVolume applies the supplied config to the custom volume.
func (b *lxdBackend) UpdateCustomVolume(volName, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "newDesc": newDesc, "newConfig": newConfig})
	logger.Debug("UpdateCustomVolume started")
	defer logger.Debug("UpdateCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	// Validate config.
	newVol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, volName, newConfig)
	err := b.driver.ValidateVolume(newVol, false)
	if err != nil {
		return err
	}

	// Get current config to compare what has changed.
	_, curVol, err := b.state.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		if err == db.ErrNoSuchObject {
			return fmt.Errorf("Volume doesn't exist")
		}

		return err
	}

	// Diff the configurations.
	changedConfig := make(map[string]string)
	userOnly := true
	for key := range curVol.Config {
		if curVol.Config[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			changedConfig[key] = newConfig[key] // Will be empty string on deleted keys.
		}
	}

	for key := range newConfig {
		if curVol.Config[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			changedConfig[key] = newConfig[key]
		}
	}

	// Apply config changes if there are any.
	if len(changedConfig) != 0 {
		curVol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, volName, curVol.Config)
		if !userOnly {
			err = b.driver.UpdateVolume(curVol, changedConfig)
			if err != nil {
				return err
			}
		}
	}

	// Check that security.unmapped and security.shifted aren't set together.
	if shared.IsTrue(newConfig["security.unmapped"]) && shared.IsTrue(newConfig["security.shifted"]) {
		return fmt.Errorf("security.unmapped and security.shifted are mutually exclusive")
	}

	// Confirm that no instances are running when changing shifted state.
	if newConfig["security.shifted"] != curVol.Config["security.shifted"] {
		usingVolume, err := VolumeUsedByInstancesWithProfiles(b.state, b.Name(), volName, db.StoragePoolVolumeTypeNameCustom, true)
		if err != nil {
			return err
		}

		if len(usingVolume) != 0 {
			return fmt.Errorf("Cannot modify shifting with running containers using the volume")
		}
	}

	// Unset idmap keys if volume is unmapped.
	if shared.IsTrue(newConfig["security.unmapped"]) {
		delete(newConfig, "volatile.idmap.last")
		delete(newConfig, "volatile.idmap.next")
	}

	// Update the database if something changed.
	if len(changedConfig) != 0 || newDesc != curVol.Description {
		err = b.state.Cluster.StoragePoolVolumeUpdate(volName, db.StoragePoolVolumeTypeCustom, b.ID(), newDesc, newConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

// DeleteCustomVolume removes a custom volume and its snapshots.
func (b *lxdBackend) DeleteCustomVolume(volName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName})
	logger.Debug("DeleteCustomVolume started")
	defer logger.Debug("DeleteCustomVolume finished")

	_, _, isSnap := shared.ContainerGetParentAndSnapshotName(volName)
	if isSnap {
		return fmt.Errorf("Volume name cannot be a snapshot")
	}

	// Retrieve a list of snapshots.
	snapshots, err := VolumeSnapshotsGet(b.state, b.name, volName, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return err
	}

	// Remove the database entry and volume from the storage device for each snapshot.
	for _, snapshot := range snapshots {
		// Extract just the snapshot name from the snapshot.
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snapshot.Name)

		// Delete the snapshot volume from the storage device.
		// Must come before Cluster.StoragePoolVolumeDelete otherwise driver won't be able
		// to get volume ID.
		err = b.driver.DeleteVolumeSnapshot(drivers.VolumeTypeCustom, volName, snapName, op)
		if err != nil {
			return err
		}

		// Remove the snapshot volume record from the database.
		// Must come after driver.DeleteVolume so that volume ID is still available.
		err = b.state.Cluster.StoragePoolVolumeDelete("default", snapshot.Name, db.StoragePoolVolumeTypeCustom, b.ID())
		if err != nil {
			return err
		}
	}

	// Delete the volume from the storage device. Must come after snapshots are removed.
	err = b.driver.DeleteVolume(drivers.VolumeTypeCustom, volName, op)
	if err != nil {
		return err
	}

	// Finally, remove the volume record from the database.
	err = b.state.Cluster.StoragePoolVolumeDelete("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		return err
	}

	return nil
}

func (b *lxdBackend) GetCustomVolumeUsage(vol api.StorageVolume) (uint64, error) {
	return 0, ErrNotImplemented
}

// SetCustomVolumeQuota modifies the custom volume's quota.
func (b *lxdBackend) SetCustomVolumeQuota(vol api.StorageVolume, quota uint64) error {
	return ErrNotImplemented
}

// MountCustomVolume mounts a custom volume.
func (b *lxdBackend) MountCustomVolume(volName string, op *operations.Operation) (bool, error) {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName})
	logger.Debug("MountCustomVolume started")
	defer logger.Debug("MountCustomVolume finished")

	return b.driver.MountVolume(drivers.VolumeTypeCustom, volName, op)
}

// UnmountCustomVolume unmounts a custom volume.
func (b *lxdBackend) UnmountCustomVolume(volName string, op *operations.Operation) (bool, error) {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName})
	logger.Debug("UnmountCustomVolume started")
	defer logger.Debug("UnmountCustomVolume finished")

	return b.driver.UnmountVolume(drivers.VolumeTypeCustom, volName, op)
}

// CreateCustomVolumeSnapshot creates a snapshot of a custom volume.
func (b *lxdBackend) CreateCustomVolumeSnapshot(volName string, newSnapshotName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "newSnapshotName": newSnapshotName})
	logger.Debug("CreateCustomVolumeSnapshot started")
	defer logger.Debug("CreateCustomVolumeSnapshot finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume cannot be snapshot")
	}

	if shared.IsSnapshot(newSnapshotName) {
		return fmt.Errorf("Snapshot name is not a valid snapshot name")
	}

	fullSnapshotName := drivers.GetSnapshotVolumeName(volName, newSnapshotName)

	// Check snapshot volume doesn't exist already.
	_, _, err := b.state.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", fullSnapshotName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != db.ErrNoSuchObject {
		if err != nil {
			return err
		}

		return fmt.Errorf("Snapshot by that name already exists")
	}

	// Load parent volume information and check it exists.
	_, parentVol, err := b.state.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		if err == db.ErrNoSuchObject {
			return fmt.Errorf("Parent volume doesn't exist")
		}

		return err
	}

	// Create database entry for new storage volume snapshot.
	err = VolumeDBCreate(b.state, b.name, fullSnapshotName, parentVol.Description, db.StoragePoolVolumeTypeNameCustom, true, parentVol.Config)
	if err != nil {
		return err
	}

	revertDB := true
	defer func() {
		if revertDB {
			b.state.Cluster.StoragePoolVolumeDelete("default", fullSnapshotName, db.StoragePoolVolumeTypeCustom, b.ID())
		}
	}()

	// Create the snapshot on the storage device.
	err = b.driver.CreateVolumeSnapshot(drivers.VolumeTypeCustom, volName, newSnapshotName, op)
	if err != nil {
		return err
	}

	revertDB = false
	return nil
}

// RenameCustomVolumeSnapshot renames a custom volume.
func (b *lxdBackend) RenameCustomVolumeSnapshot(volName string, newSnapshotName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "newSnapshotName": newSnapshotName})
	logger.Debug("RenameCustomVolumeSnapshot started")
	defer logger.Debug("RenameCustomVolumeSnapshot finished")

	parentName, oldSnapshotName, isSnap := shared.ContainerGetParentAndSnapshotName(volName)
	if !isSnap {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	if shared.IsSnapshot(newSnapshotName) {
		return fmt.Errorf("Invalid new snapshot name")
	}

	err := b.driver.RenameVolumeSnapshot(drivers.VolumeTypeCustom, parentName, oldSnapshotName, newSnapshotName, op)
	if err != nil {
		return err
	}

	newVolName := drivers.GetSnapshotVolumeName(parentName, newSnapshotName)
	err = b.state.Cluster.StoragePoolVolumeRename("default", volName, newVolName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		// Revert rename.
		b.driver.RenameVolumeSnapshot(drivers.VolumeTypeCustom, parentName, newSnapshotName, oldSnapshotName, op)
		return err
	}

	return nil
}

// DeleteCustomVolumeSnapshot removes a custom volume snapshot.
func (b *lxdBackend) DeleteCustomVolumeSnapshot(volName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName})
	logger.Debug("DeleteCustomVolumeSnapshot started")
	defer logger.Debug("DeleteCustomVolumeSnapshot finished")

	parentName, snapName, isSnap := shared.ContainerGetParentAndSnapshotName(volName)
	if !isSnap {
		return fmt.Errorf("Volume name must be a snapshot")
	}

	// Delete the snapshot from the storage device.
	err := b.driver.DeleteVolumeSnapshot(drivers.VolumeTypeCustom, parentName, snapName, op)
	if err != nil {
		return err
	}

	// Finally, remove the volume record from the database.
	err = b.state.Cluster.StoragePoolVolumeDelete("default", volName, db.StoragePoolVolumeTypeCustom, b.ID())
	if err != nil {
		return err
	}

	return nil
}

// RestoreCustomVolume restores a custom volume from a snapshot.
func (b *lxdBackend) RestoreCustomVolume(volName string, snapshotName string, op *operations.Operation) error {
	logger := logging.AddContext(b.logger, log.Ctx{"volName": volName, "snapshotName": snapshotName})
	logger.Debug("RestoreCustomVolume started")
	defer logger.Debug("RestoreCustomVolume finished")

	if shared.IsSnapshot(volName) {
		return fmt.Errorf("Volume cannot be snapshot")
	}

	if shared.IsSnapshot(snapshotName) {
		return fmt.Errorf("Invalid snapshot name")
	}

	usingVolume, err := VolumeUsedByInstancesWithProfiles(b.state, b.Name(), volName, db.StoragePoolVolumeTypeNameCustom, true)
	if err != nil {
		return err
	}

	if len(usingVolume) != 0 {
		return fmt.Errorf("Cannot restore custom volume used by running instances")
	}

	err = b.driver.RestoreVolume(b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, volName, nil), snapshotName, op)
	if err != nil {
		return err
	}

	return nil
}
