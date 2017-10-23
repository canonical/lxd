package cluster

import (
	"database/sql"
	"fmt"
	"sync/atomic"

	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/lxc/lxd/lxd/db/schema"
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
func EnsureSchema(db *sql.DB, address string) (bool, error) {
	someNodesAreBehind := false
	apiExtensions := len(version.APIExtensions)

	check := func(current int, tx *sql.Tx) error {
		// If we're bootstrapping a fresh schema, skip any check, since
		// it's safe to assume we are the only node.
		if current == 0 {
			return nil
		}

		// Check if we're clustered
		n, err := selectNodesCount(tx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch current nodes count")
		}
		if n == 0 {
			return nil // Nothing to do.
		}

		// Update the schema and api_extension columns of ourselves.
		err = updateNodeVersion(tx, address, apiExtensions)
		if err != nil {
			return errors.Wrap(err, "failed to update node version")

		}

		err = checkClusterIsUpgradable(tx, [2]int{len(updates), apiExtensions})
		if err == errSomeNodesAreBehind {
			someNodesAreBehind = true
			return schema.ErrGracefulAbort
		}
		return err
	}

	schema := Schema()
	schema.Check(check)

	_, err := schema.Ensure(db)
	if someNodesAreBehind {
		return false, nil
	}
	if err != nil {
		return false, err
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
		n, err := compareVersions(target, version)
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

// Compare two nodes versions.
//
// A version consists of the version the node's schema and the number of API
// extensions it supports.
//
// Return 0 if they equal, 1 if the first version is greater than the second
// and 2 if the second is greater than the first.
//
// Return an error if inconsistent versions are detected, for example the first
// node's schema is greater than the second's, but the number of extensions is
// smaller.
func compareVersions(version1, version2 [2]int) (int, error) {
	schema1, extensions1 := version1[0], version1[1]
	schema2, extensions2 := version2[0], version2[1]

	if schema1 == schema2 && extensions1 == extensions2 {
		return 0, nil
	}
	if schema1 >= schema2 && extensions1 >= extensions2 {
		return 1, nil
	}
	if schema1 <= schema2 && extensions1 <= extensions2 {
		return 2, nil
	}

	return -1, fmt.Errorf("nodes have inconsistent versions")
}

var errSomeNodesAreBehind = fmt.Errorf("some nodes are behind this node's version")
