package device

import (
	"fmt"
)

// ErrUnsupportedDevType is the error that occurs when an unsupported device type is created.
var ErrUnsupportedDevType = fmt.Errorf("Unsupported device type")

// ErrCannotUpdate is the error that occurs when a device cannot be updated.
var ErrCannotUpdate = fmt.Errorf("Device does not support updates")
