package storage

import (
	"fmt"
)

// ErrNilValue is the "Nil value provided" error
var ErrNilValue = fmt.Errorf("Nil value provided")

// ErrNotImplemented is the "Not implemented" error
var ErrNotImplemented = fmt.Errorf("Not implemented")

// ErrRunningQuotaResizeNotSupported is the "Running quota resize not supported" error.
var ErrRunningQuotaResizeNotSupported = fmt.Errorf("Running quota resize not supported")
