package drivers

import (
	"errors"
)

// ErrInvalidFunction is the "Invalid function" error.
var ErrInvalidFunction = errors.New("Invalid function")

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = errors.New("Unknown driver")

// ErrWatchExists is the "Watch already exists" error.
var ErrWatchExists = errors.New("Watch already exists")

// ErrInvalidPath is the "Invalid path" error.
type ErrInvalidPath struct {
	PrefixPath string
}

// Error returns the error string.
func (e *ErrInvalidPath) Error() string {
	return "Path needs to be in " + e.PrefixPath
}
