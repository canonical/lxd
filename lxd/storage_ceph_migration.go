package main

import (
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
)

type rbdMigrationSourceDriver struct {
	container        container
	snapshots        []container
	rbdSnapshotNames []string
	ceph             *storageCeph
	runningSnapName  string
	stoppedSnapName  string
}

func (s *rbdMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *rbdMigrationSourceDriver) Cleanup() {
}

func (s *rbdMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	return nil
}

func (s *rbdMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error {
	return nil
}

func (s *storageCeph) MigrationType() MigrationFSType {
	return MigrationFSType_RBD
}

func (s *storageCeph) PreservesInodes() bool {
	return false
}

func (s *storageCeph) MigrationSource(c container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return &rbdMigrationSourceDriver{}, nil
}

func (s *storageCeph) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return nil
}
