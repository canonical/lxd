package cluster

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

// Open the cluster database object.
//
// The name argument is the name of the cluster database. It defaults to
// 'db.bin', but can be overwritten for testing.
//
// The dialer argument is a function that returns a gRPC dialer that can be
// used to connect to a database node using the gRPC SQL package.
func Open(name string, dialer grpcsql.Dialer) (*sql.DB, error) {
	driver := grpcsql.NewDriver(dialer)
	driverName := grpcSQLDriverName()
	sql.Register(driverName, driver)

	// Create the cluster db. This won't immediately establish any gRPC
	// connection, that will happen only when a db transaction is started
	// (see the database/sql connection pooling code for more details).
	if name == "" {
		name = "db.bin"
	}
	db, err := sql.Open(driverName, name+"?_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("cannot open cluster database: %v", err)
	}

	return db, nil
}

// EnsureSchema applies all relevant schema updates to the cluster database.
//
// Before actually doing anything, this function will make sure that all nodes
// in the cluster have a schema version and a number of API extensions that
// match our one. If it's not the case, we either return an error (if some
// nodes have version greater than us and we need to be upgraded), or return
// false and no error (if some nodes have a lower version, and we need to wait
// till they get upgraded and restarted).
func EnsureSchema(db *sql.DB, address string, dir string) (bool, error) {
	someNodesAreBehind := false
	apiExtensions := version.APIExtensionsCount()

	check := func(current int, tx *sql.Tx) error {
		// If we're bootstrapping a fresh schema, skip any check, since
		// it's safe to assume we are the only node.
		if current == 0 {
			return nil
		}

		// Check if we're clustered
		n, err := selectUnclusteredNodesCount(tx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch unclustered nodes count")
		}
		if n > 1 {
			// This should never happen, since we only add nodes
			// with valid addresses, but check it for sanity.
			return fmt.Errorf("found more than one unclustered nodes")
		} else if n == 1 {
			address = "0.0.0.0" // We're not clustered
		}

		// Update the schema and api_extension columns of ourselves.
		err = updateNodeVersion(tx, address, apiExtensions)
		if err != nil {
			return errors.Wrap(err, "failed to update node version info")
		}

		err = checkClusterIsUpgradable(tx, [2]int{len(updates), apiExtensions})
		if err == errSomeNodesAreBehind {
			someNodesAreBehind = true
			return schema.ErrGracefulAbort
		}
		return err
	}

	schema := Schema()
	schema.File(filepath.Join(dir, "patch.global.sql")) // Optional custom queries
	schema.Check(check)

	var initial int
	err := query.Retry(func() error {
		var err error
		initial, err = schema.Ensure(db)
		return err
	})
	if someNodesAreBehind {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// When creating a database from scratch, insert an entry for node
	// 1. This is needed for referential integrity with other tables. Also,
	// create a default profile.
	if initial == 0 {
		tx, err := db.Begin()
		if err != nil {
			return false, err
		}
		stmt := `
INSERT INTO nodes(id, name, address, schema, api_extensions) VALUES(1, 'none', '0.0.0.0', ?, ?)
`
		_, err = tx.Exec(stmt, SchemaVersion, apiExtensions)
		if err != nil {
			tx.Rollback()
			return false, err
		}

		stmt = `
INSERT INTO profiles (name, description) VALUES ('default', 'Default LXD profile')
`
		_, err = tx.Exec(stmt)
		if err != nil {
			tx.Rollback()
			return false, err
		}
		err = tx.Commit()
		if err != nil {
			return false, err
		}
	}

	return true, err
}

// Generate a new name for the grpcsql driver registration. We need it to be
// unique for testing, see below.
func grpcSQLDriverName() string {
	defer atomic.AddUint64(&grpcSQLDriverSerial, 1)
	return fmt.Sprintf("grpc-%d", grpcSQLDriverSerial)
}

// Monotonic serial number for registering new instances of grpcsql.Driver
// using the database/sql stdlib package. This is needed since there's no way
// to unregister drivers, and in unit tests more than one driver gets
// registered.
var grpcSQLDriverSerial uint64

func checkClusterIsUpgradable(tx *sql.Tx, target [2]int) error {
	// Get the current versions in the nodes table.
	versions, err := selectNodesVersions(tx)
	if err != nil {
		return errors.Wrap(err, "failed to fetch current nodes versions")
	}

	for _, version := range versions {
		n, err := util.CompareVersions(target, version)
		if err != nil {
			return err
		}
		switch n {
		case 0:
			// Versions are equal, there's hope for the
			// update. Let's check the next node.
			continue
		case 1:
			// Our version is bigger, we should stop here
			// and wait for other nodes to be upgraded and
			// restarted.
			return errSomeNodesAreBehind
		case 2:
			// Another node has a version greater than ours
			// and presumeably is waiting for other nodes
			// to upgrade. Let's error out and shutdown
			// since we need a greater version.
			return fmt.Errorf("this node's version is behind, please upgrade")
		default:
			// Sanity.
			panic("unexpected return value from compareVersions")
		}
	}
	return nil
}

var errSomeNodesAreBehind = fmt.Errorf("some nodes are behind this node's version")
