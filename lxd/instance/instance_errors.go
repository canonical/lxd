package instance

import (
	"errors"
)

// ErrNotImplemented is the "Not implemented" error.
var ErrNotImplemented = errors.New("Not implemented")

// ErrMigrationTransferStarted marks a migration receive that failed after the data transfer started, so
// the instance's disks may be partly written.
var ErrMigrationTransferStarted = errors.New("Data transfer had started")
