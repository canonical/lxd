package storage

import (
	"os"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type lxdBackend struct {
	driver drivers.Driver
	id     int64
	name   string
	state  *state.State
}

func (b *lxdBackend) DaemonState() *state.State {
	return b.state
}

func (b *lxdBackend) ID() int64 {
	return b.id
}

func (b *lxdBackend) Name() string {
	return b.name
}

func (b *lxdBackend) Driver() drivers.Driver {
	return b.driver
}

func (b *lxdBackend) create(dbPool *api.StoragePool, op *operations.Operation) error {
	revertPath := true

	// Create the storage path
	path := shared.VarPath("storage-pools", b.name)
	err := os.MkdirAll(path, 0711)
	if err != nil && !os.IsExist(err) {
		return err
	}
	defer func() {
		if !revertPath {
			return
		}

		os.RemoveAll(path)
	}()

	// Create the low-level storage pool
	driver, err := drivers.Create(dbPool)
	if err != nil {
		return err
	}

	// Mount the storage pool
	ourMount, err := driver.Mount()
	if err != nil {
		return err
	}
	if ourMount {
		defer driver.Unmount()
	}

	// Create the directory structure
	err = createStorageStructure(path)
	if err != nil {
		return err
	}

	// Set the driver
	b.driver = driver
	revertPath = false

	return nil
}

func (b *lxdBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return b.driver.GetResources()
}

func (b *lxdBackend) Delete(op *operations.Operation) error {
	// Delete the low-level storage
	err := b.driver.Delete(op)
	if err != nil {
		return err
	}

	// Delete the mountpoint
	path := shared.VarPath("storage-pools", b.name)
	err = os.Remove(path)
	if err != nil {
		return err
	}

	return nil
}

func (b *lxdBackend) Mount() (bool, error) {
	return b.driver.Mount()
}

func (b *lxdBackend) Unmount() (bool, error) {
	return b.driver.Unmount()
}

func (b *lxdBackend) CreateInstance(i Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromBackup(i Instance, sourcePath string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromCopy(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromImage(i Instance, fingerprint string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceFromMigration(i Instance, conn *websocket.Conn, args migration.SinkArgs, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameInstance(i Instance, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteInstance(i Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MigrateInstance(i Instance, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, ErrNotImplemented
}

func (b *lxdBackend) RefreshInstance(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) BackupInstance(i Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) GetInstanceUsage(i Instance) (uint64, error) {
	return 0, ErrNotImplemented
}

func (b *lxdBackend) SetInstanceQuota(i Instance, quota uint64) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MountInstance(i Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) UnmountInstance(i Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) GetInstanceDisk(i Instance) (string, string, error) {
	return "", "", ErrNotImplemented
}

func (b *lxdBackend) CreateInstanceSnapshot(i Instance, name string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameInstanceSnapshot(i Instance, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteInstanceSnapshot(i Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RestoreInstanceSnapshot(i Instance, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MountInstanceSnapshot(i Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) UnmountInstanceSnapshot(i Instance) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) CreateImage(img api.Image, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteImage(img api.Image, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateCustomVolume(vol api.StorageVolume, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateCustomVolumeFromCopy(vol api.StorageVolume, src api.StorageVolume, snapshots bool, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) CreateCustomVolumeFromMigration(vol api.StorageVolume, conn *websocket.Conn, args migration.SinkArgs, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameCustomVolume(vol api.StorageVolume, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteCustomVolume(vol api.StorageVolume, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MigrateCustomVolume(vol api.StorageVolume, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, ErrNotImplemented
}

func (b *lxdBackend) GetCustomVolumeUsage(vol api.StorageVolume) (uint64, error) {
	return 0, ErrNotImplemented
}

func (b *lxdBackend) SetCustomVolumeQuota(vol api.StorageVolume, quota uint64) error {
	return ErrNotImplemented
}

func (b *lxdBackend) MountCustomVolume(vol api.StorageVolume) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) UnmountCustomVolume(vol api.StorageVolume) (bool, error) {
	return true, ErrNotImplemented
}

func (b *lxdBackend) CreateCustomVolumeSnapshot(vol api.StorageVolume, name string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) RenameCustomVolumeSnapshot(vol api.StorageVolume, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *lxdBackend) DeleteCustomVolumeSnapshot(vol api.StorageVolume, op *operations.Operation) error {
	return ErrNotImplemented
}
