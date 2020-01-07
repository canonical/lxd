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
