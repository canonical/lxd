//go:build linux && cgo && !agent

package cluster

import (
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
func Stmt(tx *sql.Tx, code int) *sql.Stmt {
	stmt, ok := PreparedStmts[code]
	if !ok {
		panic(fmt.Sprintf("No prepared statement registered with code %d", code))
	}

	return tx.Stmt(stmt)
}

// StmtString returns the in-memory query string with the given code.
func StmtString(code int) string {
	stmt, ok := stmts[code]
	if !ok {
		panic(fmt.Sprintf("No prepared statement registered with code %d", code))
	}

	return stmt
}
