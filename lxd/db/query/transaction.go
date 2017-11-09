package query

import (
	"database/sql"
	"fmt"
)

// Transaction executes the given function within a database transaction.
func Transaction(db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}

	err = f(tx)
	if err != nil {
		return rollback(tx, err)
	}

	return tx.Commit()
}

// Rollback a transaction after the given error occurred. If the rollback
// succeeds the given error is returned, otherwise a new error that wraps it
// gets generated and returned.
func rollback(tx *sql.Tx, reason error) error {
	err := tx.Rollback()
	if err != nil {
		return fmt.Errorf("failed to rollback transaction after error (%v)", reason)
	}

	return reason
}
