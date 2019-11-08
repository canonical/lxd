// +build !linux !cgo agent

package response

import (
	"database/sql"
	"os"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
)

// SmartError returns the right error message based on err.
func SmartError(err error) Response {
	if err == nil {
		return EmptySyncResponse
	}

	switch errors.Cause(err) {
	case os.ErrNotExist, sql.ErrNoRows, db.ErrNoSuchObject:
		if errors.Cause(err) != err {
			return NotFound(err)
		}

		return NotFound(nil)
	case os.ErrPermission:
		if errors.Cause(err) != err {
			return Forbidden(err)
		}

		return Forbidden(nil)
	case db.ErrAlreadyDefined:
		if errors.Cause(err) != err {
			return Conflict(err)
		}

		return Conflict(nil)
	default:
		return InternalError(err)
	}
}
