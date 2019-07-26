package device

import (
	"fmt"
)

// ErrUnsupportedDevType is the error that occurs when an unsupported device type is created.
var ErrUnsupportedDevType = fmt.Errorf("Unsupported device type")
