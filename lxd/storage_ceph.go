package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type storageCeph struct {
	storageShared
}

func (s *storageCeph) StorageCoreInit() error {
	s.sType = storageTypeCeph
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	logger.Debugf("Initializing a CEPH driver.")
	return nil
}

func (s *storageCeph) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	return nil
}

func (s *storageCeph) StoragePoolCheck() error {
	logger.Debugf("Checking CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolCreate() error {
	logger.Infof("Creating CEPH storage pool \"%s\".", s.pool.Name)
	logger.Infof("Created CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolDelete() error {
	logger.Infof("Deleting CEPH storage pool \"%s\".", s.pool.Name)
	logger.Infof("Deleted CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageCeph) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageCeph) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.StoragePoolPut
}

func (s *storageCeph) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return api.StorageVolumePut{}
}

func (s *storageCeph) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageCeph) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageCeph) GetContainerPoolInfo() (int64, string) {
	return s.poolID, s.pool.Name
}

func (s *storageCeph) StoragePoolVolumeCreate() error {
	return nil
}

func (s *storageCeph) StoragePoolVolumeDelete() error {
	return nil
}

func (s *storageCeph) StoragePoolVolumeMount() (bool, error) {
	return true, nil
}

func (s *storageCeph) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageCeph) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	return nil
}

func (s *storageCeph) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	return nil
}

func (s *storageCeph) ContainerStorageReady(name string) bool {
	return true
}

func (s *storageCeph) ContainerCreate(container container) error {
	return nil
}

func (s *storageCeph) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	return nil
}

func (s *storageCeph) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageCeph) ContainerDelete(container container) error {
	return nil
}

func (s *storageCeph) ContainerCopy(target container, source container, containerOnly bool) error {
	return nil
}

func (s *storageCeph) ContainerMount(c container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerRename(
	container container, newName string) error {

	return nil
}

func (s *storageCeph) ContainerRestore(
	container container, sourceContainer container) error {

	return nil
}

func (s *storageCeph) ContainerGetUsage(
	container container) (int64, error) {

	return 0, nil
}
func (s *storageCeph) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	return nil
}
func (s *storageCeph) ContainerSnapshotDelete(
	snapshotContainer container) error {

	return nil
}

func (s *storageCeph) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	return nil
}

func (s *storageCeph) ContainerSnapshotStart(container container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerSnapshotStop(container container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return nil
}

func (s *storageCeph) ImageCreate(fingerprint string) error {
	return nil
}

func (s *storageCeph) ImageDelete(fingerprint string) error {
	return nil
}

func (s *storageCeph) ImageMount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageCeph) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageCeph) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageCeph) PreservesInodes() bool {
	return false
}

func (s *storageCeph) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *storageCeph) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return nil
}

func (s *storageCeph) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	return fmt.Errorf("RBD storage volume quota are not supported")
}
