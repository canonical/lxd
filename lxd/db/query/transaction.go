package query

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"
)

// Transaction executes the given function within a database transaction.
func Transaction(db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}

	err = f(tx)
	if err != nil {
		return rollback(tx, err)
	}

	return tx.Commit()
}

// Rollback a transaction after the given error occured. If the rollback
// succeeds the given error is returned, otherwise a new error that wraps it
// gets generated and returned.
func rollback(tx *sql.Tx, reason error) error {
	err := tx.Rollback()
	if err != nil {
		return errors.Wrap(
			reason,
			fmt.Sprintf("failed to rollback transaction after error (%v)", reason))
	}

	return reason
}
