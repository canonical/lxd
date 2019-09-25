package instance

import (
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/shared/idmap"
)

// MigrationStorageSourceDriver defines the functions needed to implement a
// migration source driver.
type MigrationStorageSourceDriver interface {
	/* send any bits of the container/snapshots that are possible while the
	 * container is still running.
	 */
	SendWhileRunning(conn *websocket.Conn, op *operation.Operation, bwlimit string, containerOnly bool) error

	/* send the final bits (e.g. a final delta snapshot for zfs, btrfs, or
	 * do a final rsync) of the fs after the container has been
	 * checkpointed. This will only be called when a container is actually
	 * being live migrated.
	 */
	SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error

	/* Called after either success or failure of a migration, can be used
	 * to clean up any temporary snapshots, etc.
	 */
	Cleanup()

	SendStorageVolume(conn *websocket.Conn, op *operation.Operation, bwlimit string, storage Storage, volumeOnly bool) error
}

type MigrationSourceArgs struct {
	// Instance specific fields
	Instance     Instance
	InstanceOnly bool

	// Transport specific fields
	RsyncFeatures []string
	ZfsFeatures   []string

	// Volume specific fields
	VolumeOnly bool
}

type MigrationSinkArgs struct {
	// General migration fields
	Dialer  websocket.Dialer
	Push    bool
	Secrets map[string]string
	Url     string

	// Instance specific fields
	Instance     Instance
	InstanceOnly bool
	Idmap        *idmap.IdmapSet
	Live         bool
	Refresh      bool
	Snapshots    []*migration.Snapshot

	// Storage specific fields
	Storage    Storage
	VolumeOnly bool

	// Transport specific fields
	RsyncFeatures []string
}
