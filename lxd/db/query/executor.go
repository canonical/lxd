package query

import (
	"context"
	"database/sql"
)

// Executor runs statements against a database, a pinned connection or an open transaction.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ Executor = (*sql.DB)(nil)
	_ Executor = (*sql.Conn)(nil)
	_ Executor = (*sql.Tx)(nil)
	_ Executor = (*ImmediateTx)(nil)
)

// Statement runs one fixed SQL statement with the given arguments.
type Statement interface {
	ExecContext(ctx context.Context, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, args ...any) *sql.Row
}

var _ Statement = (*sql.Stmt)(nil)
