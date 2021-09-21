package query

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Transaction executes the given function within a database transaction.
// Deprecated, please use TransactionContext.
func Transaction(db *sql.DB, f func(*sql.Tx) error) error {
	return TransactionContext(context.TODO(), db, f)
}

// TransactionContext executes the given function within a database transaction.
func TransactionContext(ctx context.Context, db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		// If there is a leftover transaction let's try to rollback,
		// we'll then retry again.
		if strings.Contains(err.Error(), "cannot start a transaction within a transaction") {
			db.Exec("ROLLBACK")
		}
		return errors.Wrap(err, "failed to begin transaction")
	}

	err = f(tx)
	if err != nil {
		return rollback(tx, err)
	}

	err = tx.Commit()
	if err == sql.ErrTxDone {
		err = nil // Ignore duplicate commits/rollbacks
	}
	return err
}

// Rollback a transaction after the given error occurred. If the rollback
// succeeds the given error is returned, otherwise a new error that wraps it
// gets generated and returned.
func rollback(tx *sql.Tx, reason error) error {
	err := Retry(tx.Rollback)
	if err != nil {
		logger.Warnf("Failed to rollback transaction after error (%v): %v", reason, err)
	}

	return reason
}
