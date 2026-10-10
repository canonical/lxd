//go:build linux && cgo && !agent

//go:generate dbgen . generated.go

package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
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

// ExecutorStmt returns the statement registered under code, bound to tx: the prepared statement for a *sql.Tx, its SQL
// text otherwise, so nothing stays prepared on the server.
func ExecutorStmt(tx query.Executor, code int) (query.Statement, error) {
	if tx == nil {
		return nil, errors.New("No transaction provided")
	}

	sqlTx, isSQLTx := tx.(*sql.Tx)
	if isSQLTx {
		if sqlTx == nil {
			return nil, errors.New("No transaction provided")
		}

		return Stmt(sqlTx, code)
	}

	sqlText, err := StmtString(code)
	if err != nil {
		return nil, err
	}

	return textStmt{tx: tx, sql: sqlText}, nil
}

// textStmt runs a registered statement by sending its SQL text through an Executor.
type textStmt struct {
	tx  query.Executor
	sql string
}

// ExecContext runs the statement with args.
func (s textStmt) ExecContext(ctx context.Context, args ...any) (sql.Result, error) {
	return s.tx.ExecContext(ctx, s.sql, args...)
}

// QueryContext runs the statement as a query with args.
func (s textStmt) QueryContext(ctx context.Context, args ...any) (*sql.Rows, error) {
	return s.tx.QueryContext(ctx, s.sql, args...)
}

// QueryRowContext runs the statement as a single-row query with args.
func (s textStmt) QueryRowContext(ctx context.Context, args ...any) *sql.Row {
	return s.tx.QueryRowContext(ctx, s.sql, args...)
}

// StmtString returns the in-memory query string with the given code.
func StmtString(code int) (string, error) {
	stmt, ok := stmts[code]
	if !ok {
		return "", fmt.Errorf("No prepared statement registered with code %d", code)
	}

	return stmt, nil
}
