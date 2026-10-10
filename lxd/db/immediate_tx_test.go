//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dqlite "github.com/canonical/go-dqlite/v3"
	"github.com/canonical/go-dqlite/v3/driver"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/query"
)

// dqliteBusyTimeoutMs matches the busy timeout LXD sets on its dqlite node (lxd/cluster/gateway.go).
const dqliteBusyTimeoutMs = 5000

// newMemberPair returns two cluster database handles on one dqlite server, each with its own connection, like two LXD
// members sharing the cluster database.
func newMemberPair(t *testing.T) (m1 *sql.DB, m2 *sql.DB) {
	t.Helper()

	listener, err := net.Listen("unix", "")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	require.NoError(t, os.Mkdir(globalDir, 0o755))

	server, err := dqlite.New(uint64(1), address, globalDir, dqlite.WithBindAddress(address), dqlite.WithBusyTimeout(dqliteBusyTimeoutMs))
	require.NoError(t, err)
	require.NoError(t, server.Start())

	store, err := driver.DefaultNodeStore(":memory:")
	require.NoError(t, err)
	require.NoError(t, store.Set(context.Background(), []driver.NodeInfo{{Address: address}}))

	open := func() *db.Cluster {
		serverUUID, err := uuid.NewV7()
		require.NoError(t, err)

		cluster, err := db.OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, serverUUID.String())
		require.NoError(t, err)

		return cluster
	}

	c1 := open()
	c2 := open()

	t.Cleanup(func() {
		assert.NoError(t, c1.Close())
		assert.NoError(t, c2.Close())
		assert.NoError(t, server.Close())
	})

	_, err = c1.DB().Exec("CREATE TABLE immediate_tx_counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)")
	require.NoError(t, err)

	_, err = c1.DB().Exec("INSERT INTO immediate_tx_counter (id, value) VALUES (1, 0)")
	require.NoError(t, err)

	return c1.DB(), c2.DB()
}

// readCounterValue returns the shared counter, read outside any transaction.
func readCounterValue(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()

	var value int64
	require.NoError(t, sqlDB.QueryRow("SELECT value FROM immediate_tx_counter WHERE id = 1").Scan(&value))
	return value
}

// A member's BEGIN IMMEDIATE queues on the leader behind another member's open transaction, then sees its write.
func TestImmediateTx_QueuesAcrossMembers(t *testing.T) {
	m1, m2 := newMemberPair(t)
	ctx := context.Background()

	first, err := query.BeginImmediate(ctx, m1)
	require.NoError(t, err)

	type result struct {
		value int64
		err   error
	}

	started := make(chan struct{})
	second := make(chan result, 1)
	go func() {
		close(started)

		tx, err := query.BeginImmediate(ctx, m2)
		if err != nil {
			second <- result{err: err}
			return
		}

		var value int64
		err = tx.QueryRowContext(ctx, "SELECT value FROM immediate_tx_counter WHERE id = 1").Scan(&value)
		if err != nil {
			_ = tx.Rollback()
			second <- result{err: err}
			return
		}

		second <- result{value: value, err: tx.Commit()}
	}()

	// The value check below is the real proof: without queueing, the second snapshot would predate the update.
	<-started
	select {
	case r := <-second:
		t.Fatalf("Second member's transaction did not wait for the first: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	_, err = first.ExecContext(ctx, "UPDATE immediate_tx_counter SET value = 1 WHERE id = 1")
	require.NoError(t, err)
	require.NoError(t, first.Commit())

	select {
	case r := <-second:
		require.NoError(t, r.err)
		assert.Equal(t, int64(1), r.value)
	case <-time.After(5 * time.Second):
		t.Fatal("Second member's transaction did not resume after the first committed")
	}
}

// Two members incrementing a shared counter through ImmediateTx never see a stale snapshot and lose no increments.
func TestImmediateTx_ConcurrentReadModifyWrite(t *testing.T) {
	m1, m2 := newMemberPair(t)

	const perMember = 25

	increment := func(sqlDB *sql.DB) (busySnapshots int, err error) {
		for range perMember {
			err = query.Retry(context.Background(), func(ctx context.Context) error {
				tx, err := query.BeginImmediate(ctx, sqlDB)
				if err != nil {
					return err
				}

				var value int64
				err = tx.QueryRowContext(ctx, "SELECT value FROM immediate_tx_counter WHERE id = 1").Scan(&value)
				if err != nil {
					_ = tx.Rollback()
					return err
				}

				// Widen the gap between the read and the write so the members interleave on a fast local socket.
				time.Sleep(2 * time.Millisecond)

				_, err = tx.ExecContext(ctx, "UPDATE immediate_tx_counter SET value = ? WHERE id = 1", value+1)
				if err != nil {
					var dErr driver.Error
					if errors.As(err, &dErr) && dErr.Code == driver.ErrBusySnapshot {
						busySnapshots++
					}

					_ = tx.Rollback()
					return err
				}

				return tx.Commit()
			})
			if err != nil {
				return busySnapshots, err
			}
		}

		return busySnapshots, nil
	}

	var wg sync.WaitGroup
	var busy [2]int
	var errs [2]error

	wg.Add(2)
	go func() {
		defer wg.Done()
		busy[0], errs[0] = increment(m1)
	}()

	go func() {
		defer wg.Done()
		busy[1], errs[1] = increment(m2)
	}()

	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, 0, busy[0]+busy[1], "an IMMEDIATE transaction never reads a stale snapshot")
	assert.Equal(t, int64(2*perMember), readCounterValue(t, m1))
}
