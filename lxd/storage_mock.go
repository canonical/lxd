package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type storageMock struct {
	storageShared
}

func (s *storageMock) StorageCoreInit() (*storageCore, error) {
	sCore := storageCore{}
	sCore.sType = storageTypeMock
	typeName, err := storageTypeToString(sCore.sType)
	if err != nil {
		return nil, err
	}
	sCore.sTypeName = typeName

	err = sCore.initShared()
	if err != nil {
		return nil, err
	}

	s.storageCore = sCore

	return &sCore, nil
}

func (s *storageMock) StoragePoolInit(config map[string]interface{}) (storage, error) {
	_, err := s.StorageCoreInit()
	if err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageMock) StoragePoolCheck() error {
	return nil
}

func (s *storageMock) StoragePoolCreate() error {
	return nil
}

func (s *storageMock) StoragePoolDelete() error {
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

func (s *storageMock) ContainerPoolGet() string {
	return s.pool.Name
}

func (s *storageMock) ContainerPoolIDGet() int64 {
	return s.poolID
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

func (s *storageMock) StoragePoolVolumeUpdate(changedConfig []string) error {
	return nil
}

func (s *storageMock) StoragePoolUpdate(changedConfig []string) error {
	return nil
}

func (s *storageMock) ContainerStorageReady(name string) bool {
	return true
}

func (s *storageMock) ContainerCreate(container container) error {
	return nil
}

func (s *storageMock) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	return nil
}

func (s *storageMock) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageMock) ContainerDelete(container container) error {
	return nil
}

func (s *storageMock) ContainerCopy(
	container container, sourceContainer container) error {

	return nil
}

func (s *storageMock) ContainerMount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageMock) ContainerRename(
	container container, newName string) error {

	return nil
}

func (s *storageMock) ContainerRestore(
	container container, sourceContainer container) error {

	return nil
}

func (s *storageMock) ContainerSetQuota(
	container container, size int64) error {

	return nil
}

func (s *storageMock) ContainerGetUsage(
	container container) (int64, error) {

	return 0, nil
}
func (s *storageMock) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	return nil
}
func (s *storageMock) ContainerSnapshotDelete(
	snapshotContainer container) error {

	return nil
}

func (s *storageMock) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	return nil
}

func (s *storageMock) ContainerSnapshotStart(container container) error {
	return nil
}

func (s *storageMock) ContainerSnapshotStop(container container) error {
	return nil
}

func (s *storageMock) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return nil
}

func (s *storageMock) ImageCreate(fingerprint string) error {
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

func (s *storageMock) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageMock) PreservesInodes() bool {
	return false
}

func (s *storageMock) MigrationSource(container container) (MigrationStorageSourceDriver, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *storageMock) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	return nil
}
