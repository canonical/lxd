package storage

import "fmt"

// ErrUnsupportedStorageDriver is the error that occurs when an unsupported storage driver is detected.
var ErrUnsupportedStorageDriver = fmt.Errorf("Unsupported storage driver")
