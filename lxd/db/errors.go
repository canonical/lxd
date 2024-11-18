package db

import (
	"fmt"
	"net/http"

	"github.com/canonical/go-dqlite/driver"
	"github.com/mattn/go-sqlite3"
)

var (
	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = fmt.Errorf("No cluster member found")
)

// SmartErrors are used to return more appropriate errors to the caller.
var SmartErrors = map[int][]error{
	http.StatusConflict:           {sqlite3.ErrConstraintUnique},
	http.StatusServiceUnavailable: {driver.ErrNoAvailableLeader},
}
