package db

import (
	"errors"
	"net/http"

	"github.com/canonical/go-dqlite/v3/driver"

	"github.com/canonical/lxd/lxd/db/query"
)

var (
	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = errors.New("No cluster member found")
)

// SentinelErrors are a map of HTTP status codes to slices of sentinel errors. This is passed to [response.Init] so that
// the [response.SmartError] returns a 503 when database is not available.
var SentinelErrors = map[int][]error{
	http.StatusServiceUnavailable: {driver.ErrNoAvailableLeader},
}

// SmartErrFuncs is a slice of functions that is passed to [response.Init] so that [response.SmartError] is
// able to translate database errors into HTTP response codes. This is necessary because [driver.Error] must be
// inspected with [errors.As].
var SmartErrFuncs = []func(error) (int, string){
	func(err error) (int, string) {
		if query.IsConflictErr(err) {
			return http.StatusConflict, ""
		}

		return 0, ""
	},
}
