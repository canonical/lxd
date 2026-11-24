package db

import (
	"errors"
	"net/http"

	"github.com/canonical/go-dqlite/v3/driver"
)

var (
	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = errors.New("No cluster member found")

	// ErrImageNotAvailable is used to indicate that no online cluster member has a copy of an image.
	ErrImageNotAvailable = errors.New("Image not available on any online member")
)

// SmartErrors are used to return more appropriate errors to the caller.
var SmartErrors = map[int][]error{
	http.StatusServiceUnavailable: {driver.ErrNoAvailableLeader},
}
