package query

import (
	"strings"
	"time"

	"github.com/lxc/lxd/shared/logger"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// Retry wraps a function that interacts with the database, and retries it in
// case a transient error is hit.
//
// This should by typically used to wrap transactions.
func Retry(f func() error) error {
	// TODO: the retry loop should be configurable.
	var err error
	for i := 0; i < 20; i++ {
		err = f()
		if err != nil && IsRetriableError(err) {
			logger.Debugf("Retry failed db interaction (%v)", err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		break
	}
	return err
}

// IsRetriableError returns true if the given error might be transient and the
// interaction can be safely retried.
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}
	if err == sqlite3.ErrLocked || err == sqlite3.ErrBusy {
		return true
	}
	if err.Error() == "database is locked" {
		return true
	}

	// FIXME: we should bubble errors using errors.Wrap()
	// instead, and check for err.Cause() == sql.ErrBadConnection.
	if strings.Contains(err.Error(), "bad connection") {
		return true
	}
	if strings.Contains(err.Error(), "leadership lost") {
		return true
	}

	return false
}
