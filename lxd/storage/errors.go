package storage

import (
	"fmt"
)

// ErrNilValue is the "Nil value provided" error
var ErrNilValue = fmt.Errorf("Nil value provided")

// ErrBackupSnapshotsMismatch is the "Backup snapshots mismatch" error.
var ErrBackupSnapshotsMismatch = fmt.Errorf("Backup snapshots mismatch")

// ErrPoolUnavailable is the "Storage pool is unavailable on this server" error.
var ErrPoolUnavailable = fmt.Errorf("Storage pool is unavailable on this server")
