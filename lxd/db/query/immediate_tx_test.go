package query_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db/query"
)

// newWALDB returns a file-backed SQLite database in WAL mode with a table t(n INT) and no busy wait.
func newWALDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "immediate.db")
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=0&_journal_mode=WAL")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("CREATE TABLE t (n INT)")
	require.NoError(t, err)

	return db
}

// countRows returns the number of rows in t, read outside any transaction.
func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()

	var n int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM t").Scan(&n))
	return n
}

// requireConnReleased waits until db has no connection in use.
func requireConnReleased(t *testing.T, db *sql.DB) {
	t.Helper()

	require.Eventually(t, func() bool { return db.Stats().InUse == 0 }, 5*time.Second, 10*time.Millisecond)
}

// A committed transaction persists its writes and returns its connection to the pool.
func TestImmediateTx_Commit(t *testing.T) {
	db := newWALDB(t)

	tx, err := query.BeginImmediate(context.Background(), db)
	require.NoError(t, err)

	_, err = tx.ExecContext(context.Background(), "INSERT INTO t VALUES (?)", 1)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.Equal(t, 1, countRows(t, db))
	requireConnReleased(t, db)
	assert.Equal(t, 1, db.Stats().OpenConnections, "a cleanly ended transaction keeps its connection pooled")
}

// A rolled back transaction discards its writes.
func TestImmediateTx_Rollback(t *testing.T) {
	db := newWALDB(t)

	tx, err := query.BeginImmediate(context.Background(), db)
	require.NoError(t, err)

	_, err = tx.ExecContext(context.Background(), "INSERT INTO t VALUES (?)", 1)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	assert.Equal(t, 0, countRows(t, db))
	requireConnReleased(t, db)
}

// The transaction ends once: later Commit, Rollback and statements report that it is done.
func TestImmediateTx_UseAfterEnd(t *testing.T) {
	db := newWALDB(t)
	ctx := context.Background()

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.ErrorIs(t, tx.Commit(), sql.ErrTxDone)
	assert.ErrorIs(t, tx.Rollback(), sql.ErrTxDone)

	_, err = tx.ExecContext(ctx, "INSERT INTO t VALUES (1)")
	assert.ErrorIs(t, err, sql.ErrTxDone)

	_, err = tx.QueryContext(ctx, "SELECT n FROM t")
	assert.ErrorIs(t, err, sql.ErrTxDone)

	var n int
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n)
	assert.ErrorIs(t, err, sql.ErrConnDone)

	assert.Equal(t, 0, countRows(t, db))
}

// BEGIN IMMEDIATE takes the write lock at BEGIN, so a second writer is refused before it reads anything.
func TestImmediateTx_TakesWriteLockAtBegin(t *testing.T) {
	db := newWALDB(t)
	ctx := context.Background()

	first, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)

	_, err = query.BeginImmediate(ctx, db)
	require.Error(t, err)
	assert.True(t, query.IsRetriableError(err), "a busy BEGIN IMMEDIATE is retriable: %v", err)

	require.NoError(t, first.Rollback())

	second, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)
	require.NoError(t, second.Rollback())
}

// Cancelling the context rolls the transaction back and releases its connection.
func TestImmediateTx_ContextCancelRollsBack(t *testing.T) {
	db := newWALDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, "INSERT INTO t VALUES (1)")
	require.NoError(t, err)

	cancel()
	requireConnReleased(t, db)

	assert.ErrorIs(t, tx.Commit(), sql.ErrTxDone)
	assert.Equal(t, 0, countRows(t, db))
}

// Concurrent Commit and Rollback end the transaction exactly once.
func TestImmediateTx_ConcurrentEnd(t *testing.T) {
	db := newWALDB(t)

	tx, err := query.BeginImmediate(context.Background(), db)
	require.NoError(t, err)

	const callers = 8
	errs := make([]error, callers)

	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				errs[i] = tx.Commit()
			} else {
				errs[i] = tx.Rollback()
			}
		}()
	}

	wg.Wait()

	ended := 0
	for _, err := range errs {
		if !errors.Is(err, sql.ErrTxDone) {
			require.NoError(t, err)
			ended++
		}
	}

	assert.Equal(t, 1, ended)
	requireConnReleased(t, db)
}

// Statements from several goroutines share the transaction safely.
func TestImmediateTx_ConcurrentStatements(t *testing.T) {
	db := newWALDB(t)
	ctx := context.Background()

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)

	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tx.ExecContext(ctx, "INSERT INTO t VALUES (?)", i)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()
	require.NoError(t, tx.Commit())

	assert.Equal(t, writers, countRows(t, db))
}

// A failed final statement evicts the connection instead of returning it to the pool.
func TestImmediateTx_FailedEndEvictsConnection(t *testing.T) {
	db := newWALDB(t)
	ctx := context.Background()

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)

	// End the transaction behind ImmediateTx's back, so its COMMIT has nothing to commit.
	_, err = tx.ExecContext(ctx, "ROLLBACK")
	require.NoError(t, err)

	require.Error(t, tx.Commit())
	requireConnReleased(t, db)
	assert.Equal(t, 0, db.Stats().OpenConnections, "a connection in an unknown state must be closed, not pooled")
}

// Open rows keep the connection out of the pool until they are closed.
func TestImmediateTx_EndWaitsForOpenRows(t *testing.T) {
	db := newWALDB(t)
	ctx := context.Background()

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, "INSERT INTO t VALUES (1), (2)")
	require.NoError(t, err)

	rows, err := tx.QueryContext(ctx, "SELECT n FROM t")
	require.NoError(t, err)
	require.True(t, rows.Next())

	committed := make(chan error, 1)
	go func() { committed <- tx.Commit() }()

	select {
	case err := <-committed:
		t.Fatalf("Commit returned while rows were open: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	assert.Equal(t, 1, db.Stats().InUse, "the connection stays pinned while rows are open")

	require.NoError(t, rows.Close())
	require.NoError(t, <-committed)

	requireConnReleased(t, db)
	assert.Equal(t, 2, countRows(t, db))
}

// A pooled connection left inside a transaction is rolled back so the next BeginImmediate succeeds.
func TestImmediateTx_LeftoverTransaction(t *testing.T) {
	db := newWALDB(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "BEGIN")
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	_, err = query.BeginImmediate(ctx, db)
	require.ErrorContains(t, err, "cannot start a transaction within a transaction")
	assert.True(t, query.IsRetriableError(err))

	tx, err := query.BeginImmediate(ctx, db)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
}

// Nil inputs return errors instead of panicking.
func TestImmediateTx_NilSafety(t *testing.T) {
	ctx := context.Background()

	_, err := query.BeginImmediate(ctx, nil)
	assert.Error(t, err)

	var tx *query.ImmediateTx
	assert.ErrorIs(t, tx.Commit(), sql.ErrTxDone)
	assert.ErrorIs(t, tx.Rollback(), sql.ErrTxDone)

	_, err = tx.ExecContext(ctx, "SELECT 1")
	assert.ErrorIs(t, err, sql.ErrTxDone)

	_, err = tx.QueryContext(ctx, "SELECT 1")
	assert.ErrorIs(t, err, sql.ErrTxDone)

	var n int
	assert.ErrorIs(t, tx.QueryRowContext(ctx, "SELECT 1").Scan(&n), sql.ErrTxDone)
}
