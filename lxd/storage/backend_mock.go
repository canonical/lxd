package storage

import (
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
)

type mockBackend struct {
	name  string
	state *state.State
}

func (b *mockBackend) DaemonState() *state.State {
	return b.state
}

func (b *mockBackend) ID() int64 {
	return -1
}

func (b *mockBackend) Name() string {
	return b.name
}

func (b *mockBackend) Driver() drivers.Driver {
	return nil
}

func (b *mockBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}

func (b *mockBackend) Delete(op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateInstance(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromBackup(i Instance, sourcePath string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromCopy(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromImage(i Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromMigration(i Instance, conn *websocket.Conn, args migration.SinkArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstance(i Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstance(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateInstance(i Instance, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, nil
}

func (b *mockBackend) RefreshInstance(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) BackupInstance(i Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetInstanceUsage(i Instance) (uint64, error) {
	return 0, nil
}

func (b *mockBackend) SetInstanceQuota(i Instance, quota uint64) error {
	return nil
}

func (b *mockBackend) MountInstance(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstance(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) GetInstanceDisk(i Instance) (string, string, error) {
	return "", "", nil
}

func (b *mockBackend) CreateInstanceSnapshot(i Instance, name string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstanceSnapshot(i Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstanceSnapshot(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreInstanceSnapshot(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstanceSnapshot(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstanceSnapshot(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateImage(img api.Image, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteImage(img api.Image, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolume(vol api.StorageVolume, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromCopy(vol api.StorageVolume, src api.StorageVolume, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromMigration(vol api.StorageVolume, conn *websocket.Conn, args migration.SinkArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolume(vol api.StorageVolume, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteCustomVolume(vol api.StorageVolume, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateCustomVolume(vol api.StorageVolume, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, nil
}

func (b *mockBackend) GetCustomVolumeUsage(vol api.StorageVolume) (uint64, error) {
	return 0, nil
}

func (b *mockBackend) SetCustomVolumeQuota(vol api.StorageVolume, quota uint64) error {
	return nil
}

func (b *mockBackend) MountCustomVolume(vol api.StorageVolume) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountCustomVolume(vol api.StorageVolume) (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateCustomVolumeSnapshot(vol api.StorageVolume, name string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolumeSnapshot(vol api.StorageVolume, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteCustomVolumeSnapshot(vol api.StorageVolume, op *operations.Operation) error {
	return nil
}
