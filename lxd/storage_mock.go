package main

import (
	"io"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
)

type storageMock struct {
	storageShared
}

func (s *storageMock) StorageCoreInit() error {
	s.sType = instance.StorageTypeMock
	typeName, err := instance.StorageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	return nil
}

func (s *storageMock) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	return nil
}

func (s *storageMock) StoragePoolCheck() error {
	logger.Debugf("Checking MOCK storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageMock) StoragePoolCreate() error {
	logger.Infof("Creating MOCK storage pool \"%s\"", s.pool.Name)
	logger.Infof("Created MOCK storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageMock) StoragePoolDelete() error {
	logger.Infof("Deleting MOCK storage pool \"%s\"", s.pool.Name)
	logger.Infof("Deleted MOCK storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageMock) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageMock) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageMock) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.StoragePoolPut
}

func (s *storageMock) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return api.StorageVolumePut{}
}

func (s *storageMock) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageMock) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageMock) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.pool.Name
}

func (s *storageMock) StoragePoolVolumeCreate() error {
	return nil
}

func (s *storageMock) StoragePoolVolumeDelete() error {
	return nil
}

func (s *storageMock) StoragePoolVolumeMount() (bool, error) {
	return true, nil
}

func (s *storageMock) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageMock) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	return nil
}

func (s *storageMock) StoragePoolVolumeRename(newName string) error {
	return nil
}

func (s *storageMock) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	return nil
}

func (s *storageMock) ContainerStorageReady(container instance.Instance) bool {
	return true
}

func (s *storageMock) ContainerCreate(container instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerCreateFromImage(container instance.Instance, imageFingerprint string, tracker *ioprogress.ProgressTracker) error {
	return nil
}

func (s *storageMock) ContainerDelete(container instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerCopy(target instance.Instance, source instance.Instance, containerOnly bool) error {
	return nil
}

func (s *storageMock) ContainerRefresh(target instance.Instance, source instance.Instance, snapshots []instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerMount(c instance.Instance) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerUmount(c instance.Instance, path string) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerRename(container instance.Instance, newName string) error {
	return nil
}

func (s *storageMock) ContainerRestore(container instance.Instance, sourceContainer instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerGetUsage(container instance.Instance) (int64, error) {
	return 0, nil
}
func (s *storageMock) ContainerSnapshotCreate(snapshotContainer instance.Instance, sourceContainer instance.Instance) error {
	return nil
}
func (s *storageMock) ContainerSnapshotDelete(snapshotContainer instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerSnapshotRename(snapshotContainer instance.Instance, newName string) error {
	return nil
}

func (s *storageMock) ContainerSnapshotStart(container instance.Instance) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerSnapshotStop(container instance.Instance) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerSnapshotCreateEmpty(snapshotContainer instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerBackupCreate(backup instance.Backup, sourceContainer instance.Instance) error {
	return nil
}

func (s *storageMock) ContainerBackupLoad(info instance.BackupInfo, data io.ReadSeeker, tarArgs []string) error {
	return nil
}

func (s *storageMock) ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error {
	return nil
}

func (s *storageMock) ImageDelete(fingerprint string) error {
	return nil
}

func (s *storageMock) ImageMount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageMock) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageMock) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (s *storageMock) PreservesInodes() bool {
	return false
}

func (s *storageMock) MigrationSource(args instance.MigrationSourceArgs) (instance.MigrationStorageSourceDriver, error) {
	return nil, nil
}

func (s *storageMock) MigrationSink(conn *websocket.Conn, op *operation.Operation, args instance.MigrationSinkArgs) error {
	return nil
}

func (s *storageMock) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	return nil
}

func (s *storageMock) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	return &api.ResourcesStoragePool{}, nil
}

func (s *storageMock) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	return nil
}

func (s *storageMock) StorageMigrationSource(args instance.MigrationSourceArgs) (instance.MigrationStorageSourceDriver, error) {
	return nil, nil
}

func (s *storageMock) StorageMigrationSink(conn *websocket.Conn, op *operation.Operation, args instance.MigrationSinkArgs) error {
	return nil
}

func (s *storageMock) GetStoragePool() *api.StoragePool {
	return nil
}

func (s *storageMock) GetStoragePoolVolume() *api.StorageVolume {
	return nil
}

func (s *storageMock) GetState() *state.State {
	return nil
}

func (s *storageMock) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	return nil
}

func (s *storageMock) StoragePoolVolumeSnapshotDelete() error {
	return nil
}

func (s *storageMock) StoragePoolVolumeSnapshotRename(newName string) error {
	return nil
}
