package drivers

import (
	"fmt"
)

// ErrNotImplemented is the "Not implemented" error
var ErrNotImplemented = fmt.Errorf("Not implemented")

// ErrUnknownDriver is the "Unknown driver" error
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

// ErrNotSupported is the "Not supported" error
var ErrNotSupported = fmt.Errorf("Not supported")

// ErrDeleteSnapshots is a special error used to tell the backend to delete more recent snapshots
type ErrDeleteSnapshots struct {
	Snapshots []string
}

func (e ErrDeleteSnapshots) Error() string {
	return fmt.Sprintf("More recent snapshots must be deleted: %+v", e.Snapshots)
}
