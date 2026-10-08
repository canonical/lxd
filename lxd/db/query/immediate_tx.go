package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// immediateTxEndTimeout bounds the COMMIT or ROLLBACK that ends an ImmediateTx.
const immediateTxEndTimeout = 5 * time.Second

// ImmediateTx is a BEGIN IMMEDIATE transaction that owns a pinned connection until it ends.
type ImmediateTx struct {
	mu     sync.RWMutex // Statements hold the read lock; ending the transaction takes the write lock.
	done   bool
	conn   *sql.Conn
	cancel context.CancelFunc
}

// BeginImmediate pins a connection from db, starts BEGIN IMMEDIATE on it, and rolls back if ctx ends first.
func BeginImmediate(ctx context.Context, db *sql.DB) (*ImmediateTx, error) {
	if db == nil {
		return nil, errors.New("Failed beginning transaction: No database provided")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed getting connection: %w", err)
	}

	_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
	if err != nil {
		// A pooled connection with a leftover transaction needs a rollback before it can be reused.
		if strings.Contains(err.Error(), "cannot start a transaction within a transaction") {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}

		_ = conn.Close()
		return nil, fmt.Errorf("Failed beginning transaction: %w", err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	tx := &ImmediateTx{conn: conn, cancel: cancel}

	go func() {
		<-watchCtx.Done()
		_ = tx.end("ROLLBACK")
	}()

	return tx, nil
}

// ExecContext runs a statement inside the transaction.
func (tx *ImmediateTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx == nil {
		return nil, sql.ErrTxDone
	}

	tx.mu.RLock()
	defer tx.mu.RUnlock()

	if tx.done {
		return nil, sql.ErrTxDone
	}

	return tx.conn.ExecContext(ctx, query, args...)
}

// QueryContext runs a query inside the transaction; the transaction cannot release its connection until the rows are closed.
func (tx *ImmediateTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx == nil {
		return nil, sql.ErrTxDone
	}

	tx.mu.RLock()
	defer tx.mu.RUnlock()

	if tx.done {
		return nil, sql.ErrTxDone
	}

	return tx.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a single-row query inside the transaction; after the transaction ends the row reports sql.ErrConnDone.
func (tx *ImmediateTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx == nil {
		return txDoneRow(ctx)
	}

	tx.mu.RLock()
	defer tx.mu.RUnlock()

	// A closed *sql.Conn never reaches the pool again, so querying it after the end only yields sql.ErrConnDone.
	return tx.conn.QueryRowContext(ctx, query, args...)
}

// Commit commits the transaction and returns the connection to the pool.
func (tx *ImmediateTx) Commit() error {
	if tx == nil {
		return sql.ErrTxDone
	}

	return tx.end("COMMIT")
}

// Rollback rolls back the transaction; calling it after Commit returns sql.ErrTxDone.
func (tx *ImmediateTx) Rollback() error {
	if tx == nil {
		return sql.ErrTxDone
	}

	return tx.end("ROLLBACK")
}

// end runs the final statement once, evicts the connection if that statement fails, and releases the connection.
func (tx *ImmediateTx) end(stmt string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.done {
		return sql.ErrTxDone
	}

	tx.done = true
	tx.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), immediateTxEndTimeout)
	defer cancel()

	_, err := tx.conn.ExecContext(ctx, stmt)
	if err != nil {
		// The transaction state is unknown, so keep the connection out of the pool.
		_ = tx.conn.Raw(func(any) error { return driver.ErrBadConn })
	}

	// Close waits for open rows, then returns the connection to the pool unless it was evicted above.
	_ = tx.conn.Close()

	if err != nil {
		return fmt.Errorf("Failed ending transaction with %s: %w", stmt, err)
	}

	return nil
}

// txDoneDB is a database whose connections always fail with sql.ErrTxDone.
var txDoneDB = sync.OnceValue(func() *sql.DB { return sql.OpenDB(txDoneConnector{}) })

// txDoneRow returns a row whose Scan reports sql.ErrTxDone.
func txDoneRow(ctx context.Context) *sql.Row {
	return txDoneDB().QueryRowContext(ctx, "")
}

// txDoneConnector is a driver.Connector that refuses every connection with sql.ErrTxDone.
type txDoneConnector struct{}

// Connect always fails with sql.ErrTxDone.
func (txDoneConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, sql.ErrTxDone
}

// Driver returns a driver that refuses every connection with sql.ErrTxDone.
func (txDoneConnector) Driver() driver.Driver {
	return txDoneDriver{}
}

// txDoneDriver is a driver.Driver that refuses every connection with sql.ErrTxDone.
type txDoneDriver struct{}

// Open always fails with sql.ErrTxDone.
func (txDoneDriver) Open(string) (driver.Conn, error) {
	return nil, sql.ErrTxDone
}
