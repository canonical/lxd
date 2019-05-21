package bindings

/*
#include <sqlite3.h>
*/
import "C"
import (
	"github.com/pkg/errors"
)

// Error holds information about a SQLite error.
type Error struct {
	Code    int
	Message string
}

func (e Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return C.GoString(C.sqlite3_errstr(C.int(e.Code)))
}

// Error codes.
const (
	ErrError               = C.SQLITE_ERROR
	ErrInternal            = C.SQLITE_INTERNAL
	ErrNoMem               = C.SQLITE_NOMEM
	ErrInterrupt           = C.SQLITE_INTERRUPT
	ErrBusy                = C.SQLITE_BUSY
	ErrIoErr               = C.SQLITE_IOERR
	ErrIoErrNotLeader      = C.SQLITE_IOERR_NOT_LEADER
	ErrIoErrLeadershipLost = C.SQLITE_IOERR_LEADERSHIP_LOST
)

// ErrorCode extracts a SQLite error code from a Go error.
func ErrorCode(err error) int {
	if err, ok := errors.Cause(err).(Error); ok {
		return err.Code
	}

	// Return a generic error.
	return int(C.SQLITE_ERROR)
}

// Create a Go error with the code and message of the last error happened on
// the given database.
func lastError(db *C.sqlite3) Error {
	return Error{
		Code:    int(C.sqlite3_extended_errcode(db)),
		Message: C.GoString(C.sqlite3_errmsg(db)),
	}
}

// codeToError converts a SQLite error code to a Go error.
func codeToError(rc C.int) error {
	return Error{Code: int(rc)}
}
