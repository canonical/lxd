package storage

import (
	"fmt"
)

// ErrNilValue is the "Nil value provided" error
var ErrNilValue = fmt.Errorf("Nil value provided")

// ErrNotImplemented is the "Not implemented" error
var ErrNotImplemented = fmt.Errorf("Not implemented")

// ErrBackupSnapshotsMismatch is the "Backup snapshots mismatch" error.
var ErrBackupSnapshotsMismatch = fmt.Errorf("Backup snapshots mismatch")
