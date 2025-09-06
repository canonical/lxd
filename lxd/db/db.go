//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/canonical/go-dqlite/v3/driver"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/node"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// DB represents access to LXD's global and local databases.
type DB struct {
	Node    *Node
	Cluster *Cluster
}

// Node mediates access to LXD's data stored in the node-local SQLite database.
type Node struct {
	db  *sql.DB // Handle to the node-local SQLite database file.
	dir string  // Reference to the directory where the database file lives.
}

// Transactor is used to run transactions against the cluster database.
type Transactor func(ctx context.Context, f func(context.Context, *ClusterTx) error) error

// OpenNode creates a new Node object.
//
// The fresh hook parameter is used by the daemon to mark all known patch names
// as applied when a brand new database is created.
//
// Return the newly created Node object.
func OpenNode(dir string, fresh func(*Node) error) (*Node, error) {
	db, err := node.Open(dir)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	initial, err := node.EnsureSchema(db, dir)
	if err != nil {
		return nil, err
	}

	node := &Node{
		db:  db,
		dir: dir,
	}

	if initial == 0 {
		if fresh != nil {
			err := fresh(node)
			if err != nil {
				return nil, err
			}
		}
	}

	return node, nil
}

// DirectAccess is a bit of a hack which allows getting a database Node struct from any standard Go sql.DB.
// This is primarily used to access the "db.bin" read-only copy of the database during startup.
func DirectAccess(db *sql.DB) *Node {
	return &Node{db: db}
}

// DB returns the low level database handle to the node-local SQLite
// database.
//
//	FIXME: this is used for compatibility with some legacy code, and should be
//		dropped once there are no call sites left.
func (n *Node) DB() *sql.DB {
	return n.db
}

// Dir returns the directory of the underlying SQLite database file.
func (n *Node) Dir() string {
	return n.dir
}

// DqliteDir returns the global database directory used by dqlite.
func (n *Node) DqliteDir() string {
	return filepath.Join(n.Dir(), "global")
}

// Transaction creates a new NodeTx object and transactionally executes the
// node-level database interactions invoked by the given function. If the
// function returns no error, all database changes are committed to the
// node-level database, otherwise they are rolled back.
func (n *Node) Transaction(ctx context.Context, f func(context.Context, *NodeTx) error) error {
	nodeTx := &NodeTx{}
	return query.Transaction(ctx, n.db, func(ctx context.Context, tx *sql.Tx) error {
		nodeTx.tx = tx
		return f(ctx, nodeTx)
	})
}

// Close the database facade.
func (n *Node) Close() error {
	return n.db.Close()
}

// Cluster mediates access to LXD's data stored in the cluster dqlite database.
type Cluster struct {
	db         *sql.DB // Handle to the cluster dqlite database, gated behind gRPC SQL.
	nodeID     int64   // Node ID of this LXD instance.
	mu         sync.RWMutex
	closingCtx context.Context
}

// OpenCluster creates a new Cluster object for interacting with the dqlite
// database.
//
// - name: Basename of the database file holding the data. Typically "db.bin".
// - dialer: Function used to connect to the dqlite backend via gRPC SQL.
// - address: Network address of this node (or empty string).
// - dir: Base LXD database directory (e.g. /var/lib/lxd/database)
// - timeout: Give up trying to open the database after this amount of time.
// - dump: If not nil, a copy of 2.0 db data, for migrating to 3.0.
//
// The address and api parameters will be used to determine if the cluster
// database matches our version, and possibly trigger a schema update. If the
// schema update can't be performed right now, because some nodes are still
// behind, an Upgrading error is returned.
// Accepts a closingCtx context argument used to indicate when the daemon is shutting down.
func OpenCluster(closingCtx context.Context, name string, store driver.NodeStore, address, dir string, timeout time.Duration, dump *Dump, serverUUID string, options ...driver.Option) (*Cluster, error) {
	db, err := cluster.Open(name, store, options...)
	if err != nil {
		return nil, fmt.Errorf("Failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Test that the cluster database is operational. We wait up to the
	// given timeout , in case there's no quorum of nodes online yet.
	connectCtx, connectCancel := context.WithTimeout(closingCtx, timeout)
	defer connectCancel()
	for i := 0; ; i++ {
		// Log initial attempts at debug level, but use warn
		// level after the 5'th attempt (about 10 seconds).
		// After the 15'th attempt (about 30 seconds), log
		// only one attempt every 5.
		logPriority := 1 // 0 is discard, 1 is Debug, 2 is Error
		if i > 5 {
			logPriority = 2
			if i > 15 && (i%5) != 0 {
				logPriority = 0
			}
		}

		logger.Info("Connecting to global database")
		pingCtx, pingCancel := context.WithTimeout(connectCtx, time.Second*5)
		err = db.PingContext(pingCtx)
		pingCancel()
		logCtx := logger.Ctx{"err": err, "attempt": i}
		if err != nil && !errors.Is(err, driver.ErrNoAvailableLeader) {
			return nil, err
		} else if err == nil {
			logger.Info("Connected to global database")
			break
		}

		switch logPriority {
		case 1:
			logger.Debug("Failed connecting to global database", logCtx)
		case 2:
			logger.Error("Failed connecting to global database", logCtx)
		}

		err = connectCtx.Err()
		if err != nil {
			return nil, err
		}

		time.Sleep(2 * time.Second)
	}

	// FIXME: https://github.com/canonical/dqlite/issues/163
	_, err = db.Exec("PRAGMA cache_size=-50000")
	if err != nil {
		return nil, fmt.Errorf("Failed to set page cache size: %w", err)
	}

	if dump != nil {
		logger.Info("Migrating data from local to global database")
		err := query.Transaction(closingCtx, db, func(ctx context.Context, tx *sql.Tx) error {
			return importPreClusteringData(tx, dump)
		})
		if err != nil {
			// Restore the local sqlite3 backup and wipe the raft
			// directory, so users can fix problems and retry.
			path := filepath.Join(dir, "local.db")
			copyErr := shared.FileCopy(path+".bak", path)
			if copyErr != nil {
				// Ignore errors here, there's not much we can do
				logger.Errorf("Failed to restore local database: %v", copyErr)
			}

			rmErr := os.RemoveAll(filepath.Join(dir, "global"))
			if rmErr != nil {
				// Ignore errors here, there's not much we can do
				logger.Errorf("Failed to cleanup global database: %v", rmErr)
			}

			return nil, fmt.Errorf("Failed to migrate data to global database: %w", err)
		}
	}

	err = cluster.EnsureSchema(db, address, dir, serverUUID)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusPreconditionFailed) {
			cluster := &Cluster{
				db:         db,
				closingCtx: closingCtx,
			}

			return cluster, err
		}

		return nil, fmt.Errorf("Failed to ensure schema: %w", err)
	}

	stmts, err := cluster.PrepareStmts(db, false)
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare statements: %w", err)
	}

	cluster.PreparedStmts = stmts

	clusterDB := &Cluster{
		db:         db,
		closingCtx: closingCtx,
	}

	err = clusterDB.Transaction(closingCtx, func(ctx context.Context, tx *ClusterTx) error {
		// Figure out the ID of this node.
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		memberID := int64(-1)
		if len(members) == 1 && members[0].Address == "0.0.0.0" {
			// We're not clustered
			memberID = 1
		} else {
			for _, member := range members {
				if member.Address == address {
					memberID = member.ID
					break
				}
			}
		}

		if memberID < 0 {
			return fmt.Errorf("No node registered with address %s", address)
		}

		// Set the local member ID
		clusterDB.NodeID(memberID)

		// Delete any operation tied to this member
		err = cluster.DeleteOperations(ctx, tx.tx, memberID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return clusterDB, nil
}

// ForLocalInspection is a aid for the hack in initializeDbObject, which
// sets the db-related Deamon attributes upfront, to be backward compatible
// with the legacy patches that need to interact with the database.
func ForLocalInspection(db *sql.DB) *Cluster {
	return &Cluster{
		db:         db,
		closingCtx: context.Background(),
	}
}

// ForLocalInspectionWithPreparedStmts is the same as ForLocalInspection but it
// also prepares the statements used in auto-generated database code.
func ForLocalInspectionWithPreparedStmts(db *sql.DB) (*Cluster, error) {
	c := ForLocalInspection(db)

	stmts, err := cluster.PrepareStmts(c.db, true)
	if err != nil {
		return nil, fmt.Errorf("Prepare database statements: %w", err)
	}

	cluster.PreparedStmts = stmts

	return c, nil
}

// GetNodeID returns the current nodeID (0 if not set).
func (c *Cluster) GetNodeID() int64 {
	return c.nodeID
}

// Transaction creates a new ClusterTx object and transactionally executes the
// cluster database interactions invoked by the given function. If the function
// returns no error, all database changes are committed to the cluster database
// database, otherwise they are rolled back.
//
// If EnterExclusive has been called before, calling Transaction will block
// until ExitExclusive has been called as well to release the lock.
func (c *Cluster) Transaction(ctx context.Context, f func(context.Context, *ClusterTx) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transaction(ctx, f)
}

// RunExclusive acquires a lock on the cluster db and calls f.
// Any successive call to Transaction() will block until f has returned.
// f is passed a Transactor that can be used to run transactions against the locked database.
func (c *Cluster) RunExclusive(f func(t Transactor) error) error {
	logger.Info("Acquiring exclusive lock on cluster database")

	unlock := func() {
		logger.Info("Releasing exclusive lock on cluster database")
		c.mu.Unlock()
	}

	timeout := 20 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ch := make(chan struct{})
	go func() {
		c.mu.Lock()

		if ctx.Err() == nil {
			// Close channel to indicate lock acquired. Safe even if outer function has returned.
			close(ch)
		} else {
			// Release lock if outer function has timed out and returned.
			// This avoids leaving the lock acquired permanently in the case of timeout.
			unlock()
		}
	}()

	select {
	case <-ch:
		err := f(c.transaction)
		unlock()
		return err
	case <-time.After(timeout):
		return fmt.Errorf("Timed out exclusively locking cluster database (%s)", timeout)
	}
}

func (c *Cluster) transaction(ctx context.Context, f func(context.Context, *ClusterTx) error) error {
	clusterTx := &ClusterTx{
		nodeID: c.nodeID,
	}

	return query.Retry(ctx, func(ctx context.Context) error {
		txFunc := func(ctx context.Context, tx *sql.Tx) error {
			clusterTx.tx = tx
			return f(ctx, clusterTx)
		}

		err := query.Transaction(ctx, c.db, txFunc)
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			// If the query timed out it likely means that the leader has abruptly become unreachable.
			// Now that this query has been cancelled, a leader election should have taken place by now.
			// So let's retry the transaction once more in case the global database is now available again.
			logger.Warn("Transaction timed out. Retrying once", logger.Ctx{"member": c.nodeID, "err": err})
			return query.Transaction(ctx, c.db, txFunc)
		}

		return err
	})
}

// NodeID sets the node NodeID associated with this cluster instance. It's used for
// backward-compatibility of all db-related APIs that were written before
// clustering and don't accept a node NodeID, so in those cases we automatically
// use this value as implicit node NodeID.
func (c *Cluster) NodeID(id int64) {
	c.nodeID = id
}

// Close the database facade.
func (c *Cluster) Close() error {
	for _, stmt := range cluster.PreparedStmts {
		_ = stmt.Close()
	}

	return c.db.Close()
}

// DB returns the low level database handle to the cluster database.
//
//	FIXME: this is used for compatibility with some legacy code, and should be
//		dropped once there are no call sites left.
func (c *Cluster) DB() *sql.DB {
	return c.db
}

// Begin a new transaction against the cluster database.
//
// FIXME: legacy method.
func (c *Cluster) Begin() (*sql.Tx, error) {
	return begin(c.db)
}

func begin(db *sql.DB) (*sql.Tx, error) {
	for range 1000 {
		tx, err := db.Begin()
		if err == nil {
			return tx, nil
		}

		if !query.IsRetriableError(err) {
			logger.Debugf("DbBegin: error %q", err)
			return nil, err
		}

		time.Sleep(30 * time.Millisecond)
	}

	logger.Debug("DbBegin: DB still locked")
	logger.Debug(logger.GetStack())
	return nil, errors.New("DB is locked")
}

// TxCommit commits the given transaction.
func TxCommit(tx *sql.Tx) error {
	err := tx.Commit()
	if err == nil || err == sql.ErrTxDone { // Ignore duplicate commits/rollbacks
		return nil
	}

	return err
}

// DqliteLatestSegment returns the latest segment ID in the global database.
func DqliteLatestSegment() (string, error) {
	dir := shared.VarPath("database", "global")
	file, err := os.Open(dir)
	if err != nil {
		return "", fmt.Errorf("Unable to open directory %s with error %v", dir, err)
	}

	defer func() { _ = file.Close() }()

	fileNames, err := file.Readdirnames(0)
	if err != nil {
		return "", fmt.Errorf("Unable to read file names in directory %s with error %v", dir, err)
	}

	if len(fileNames) == 0 {
		return "none", nil
	}

	sort.Strings(fileNames)

	r, err := regexp.Compile(`^[0-9]+-[0-9]+$`)
	if err != nil {
		return "none", err
	}

	for i := range fileNames {
		fileName := fileNames[len(fileNames)-1-i]
		if r.MatchString(fileName) {
			segment := strings.Split(fileName, "-")[1]
			// Trim leading o's.
			index := 0
			for i, c := range segment {
				index = i
				if c != '0' {
					break
				}
			}

			return segment[index:], nil
		}
	}

	return "none", nil
}

func dbQueryRowScan(ctx context.Context, c *ClusterTx, q string, args []any, outargs []any) error {
	return c.tx.QueryRowContext(ctx, q, args...).Scan(outargs...)
}
