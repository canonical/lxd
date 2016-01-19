package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageMock struct {
	d     *Daemon
	sType storageType
	log   log.Logger

	storageShared
}

func (s *storageMock) Init(config map[string]interface{}) (storage, error) {
	s.sType = storageTypeMock
	s.sTypeName = storageTypeToString(storageTypeMock)

	if err := s.initShared(); err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageMock) GetStorageType() storageType {
	return s.sType
}

func (s *storageMock) GetStorageTypeName() string {
	return s.sTypeName
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

func (s *storageMock) ContainerStart(container container) error {
	return nil
}

func (s *storageMock) ContainerStop(container container) error {
	return nil
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

func (s *storageMock) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageMock) MigrationSource(container container) ([]MigrationStorageSource, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *storageMock) MigrationSink(container container, snapshots []container, conn *websocket.Conn) error {
	return nil
}
