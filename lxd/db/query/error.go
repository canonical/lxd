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

// ErrMultipleMatches should be used where a query expected to return or modify a single row, but more than one row
// is returned/modified.
type ErrMultipleMatches struct {
	err error
}

func (e ErrMultipleMatches) defaultErrMsg() string {
	return "Multiple matches were found but only one was expected"
}

// Error implements the error builtin, making ErrMultipleMatches a valid error type.
func (e ErrMultipleMatches) Error() string {
	if e.err == nil {
		return e.defaultErrMsg()
	}

	return e.err.Error()
}

// Unwrap implements [errors.Unwrap] for error cause inspection.
func (e ErrMultipleMatches) Unwrap() error {
	if e.err == nil {
		return errors.New(e.defaultErrMsg())
	}

	return e.err
}

// NewMultipleMatchErr returns a new [ErrMultipleMatches] with the given error as a cause.
func NewMultipleMatchErr(err error) ErrMultipleMatches {
	return ErrMultipleMatches{err: err}
}

// IsMultipleMatchErr returns true if the given error is an [ErrMultipleMatches] and false otherwise.
func IsMultipleMatchErr(err error) bool {
	_, ok := errors.AsType[ErrMultipleMatches](err)
	return ok
}
