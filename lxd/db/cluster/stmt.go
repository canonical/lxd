//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
)

// RegisterStmt register a SQL statement.
//
// Registered statements will be prepared upfront and re-used, to speed up
// execution.
//
// Return a unique registration code.
func RegisterStmt(sql string) int {
	code := len(stmts)
	stmts[code] = sql
	return code
}

// PrepareStmts prepares all registered statements and returns an index from
// statement code to prepared statement object.
func PrepareStmts(db *sql.DB, skipErrors bool) (map[int]*sql.Stmt, error) {
	index := map[int]*sql.Stmt{}

	for code, sql := range stmts {
		stmt, err := db.Prepare(sql)
		if err != nil && !skipErrors {
			return nil, fmt.Errorf("%q: %w", sql, err)
		}

		index[code] = stmt
	}

	return index, nil
}

var stmts = map[int]string{} // Statement code to statement SQL text.

// PreparedStmts is a placeholder for transitioning to package-scoped transaction functions.
var PreparedStmts = map[int]*sql.Stmt{}

// Stmt prepares the in-memory prepared statement for the transaction.
func Stmt(tx *sql.Tx, code int) (*sql.Stmt, error) {
	stmt, ok := PreparedStmts[code]
	if !ok {
		return nil, fmt.Errorf("No prepared statement registered with code %d", code)
	}

	return tx.Stmt(stmt), nil
}

// StmtString returns the in-memory query string with the given code.
func StmtString(code int) (string, error) {
	stmt, ok := stmts[code]
	if !ok {
		return "", fmt.Errorf("No prepared statement registered with code %d", code)
	}

	return stmt, nil
}

// applyTriggers adds triggers to the database.
//
// Warning: These triggers are applied separately to the schema update mechanism. Changes to these triggers (especially their names)
// may require a patch.
func applyTriggers(ctx context.Context, tx *sql.Tx) error {
	for _, entityType := range entityTypes {
		if entityType.OnDeleteTriggerName() != "" {
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s`, entityType.OnDeleteTriggerName()))
			if err != nil {
				return err
			}

			_, err = tx.ExecContext(ctx, entityType.OnDeleteTriggerSQL())
			if err != nil {
				return err
			}
		}
	}

	return nil
}
