package shared

import (
	"strings"
)

// ShuttingDownError is returned by the LXD daemon while it is shutting down.
const ShuttingDownError = "LXD is shutting down"

// IsShuttingDownError returns true if `err` contains ShuttingDownError.
func IsShuttingDownError(err error) bool {
	return strings.Contains(err.Error(), ShuttingDownError)
}
