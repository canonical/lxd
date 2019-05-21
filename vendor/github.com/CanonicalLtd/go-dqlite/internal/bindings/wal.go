package bindings

/*
#include <stdlib.h>
#include <sqlite3.h>
*/
import "C"
import "unsafe"

// WalCheckpointMode defines all valid values for the "checkpoint mode" parameter
// of the WalCheckpointV2 API. See https://sqlite.org/c3ref/wal_checkpoint_v2.html.
type WalCheckpointMode int

// WAL checkpoint modes
const (
	WalCheckpointPassive  = WalCheckpointMode(C.SQLITE_CHECKPOINT_PASSIVE)
	WalCheckpointFull     = WalCheckpointMode(C.SQLITE_CHECKPOINT_FULL)
	WalCheckpointRestart  = WalCheckpointMode(C.SQLITE_CHECKPOINT_RESTART)
	WalCheckpointTruncate = WalCheckpointMode(C.SQLITE_CHECKPOINT_TRUNCATE)
)

// WalCheckpoint triggers a WAL checkpoint on the given database attached to the
// connection. See https://sqlite.org/c3ref/wal_checkpoint_v2.html
func (c *Conn) WalCheckpoint(schema string, mode WalCheckpointMode) (int, int, error) {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	var size C.int
	var ckpt C.int
	var err error

	// Convert to C types
	zDb := C.CString(schema)
	defer C.free(unsafe.Pointer(zDb))

	rc := C.sqlite3_wal_checkpoint_v2(db, zDb, C.int(mode), &size, &ckpt)
	if rc != 0 {
		return -1, -1, lastError(db)
	}

	return int(size), int(ckpt), err
}
