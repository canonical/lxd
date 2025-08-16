package query

import (
	"errors"
	"slices"

	"github.com/canonical/go-dqlite/v3/driver"
	"github.com/mattn/go-sqlite3"
)

// conflictErrorCodes are a list of sqlite3 error codes that should be represented at API level as a 409 Conflict.
var conflictErrorCodes = []int{
	int(sqlite3.ErrConstraintUnique),
	int(sqlite3.ErrConstraintTrigger),
}

// IsConflictErr returns true if the given error represents a constraint violation that should be represented at API
// level as a 409 Conflict.
func IsConflictErr(err error) bool {
	var driverErr driver.Error
	if !errors.As(err, &driverErr) {
		return false
	}

	return slices.Contains(conflictErrorCodes, driverErr.Code)
}
