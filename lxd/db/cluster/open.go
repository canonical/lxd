package cluster

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"

	driver "github.com/canonical/go-dqlite/driver"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
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
func Open(name string, store driver.NodeStore, options ...driver.Option) (*sql.DB, error) {
	driver, err := driver.New(store, options...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create dqlite driver")
	}

	driverName := dqliteDriverName()
	sql.Register(driverName, driver)

	// Create the cluster db. This won't immediately establish any network
	// connection, that will happen only when a db transaction is started
	// (see the database/sql connection pooling code for more details).
	if name == "" {
		name = "db.bin"
	}
	db, err := sql.Open(driverName, name)
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

	backupDone := false
	hook := (func(version int, tx *sql.Tx) error {
		// Check if this is a fresh instance.
		isUpdate, err := schema.DoesSchemaTableExist(tx)
		if err != nil {
			return errors.Wrap(err, "failed to check if schema table exists")
		}

		if !isUpdate {
			return nil
		}

		// Check if we're clustered
		clustered := true
		n, err := selectUnclusteredNodesCount(tx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch unclustered nodes count")
		}
		if n > 1 {
			// This should never happen, since we only add nodes with valid addresses..
			return fmt.Errorf("found more than one unclustered nodes")
		} else if n == 1 {
			clustered = false
		}

		// If we're not clustered, backup the local cluster database directory
		// before performing any schema change. This makes sense only in the
		// non-clustered case, because otherwise the directory would be
		// re-populated by replication.
		if !clustered && !backupDone {
			logger.Infof("Updating the LXD global schema. Backup made as \"global.bak\"")
			err := shared.DirCopy(
				filepath.Join(dir, "global"),
				filepath.Join(dir, "global.bak"),
			)
			if err != nil {
				return errors.Wrap(err, "failed to backup global database")
			}
			backupDone = true
		}

		if version == -1 {
			logger.Debugf("Running pre-update queries from file for global DB schema")
		} else {
			logger.Debugf("Updating global DB schema from %d to %d", version, version+1)
		}

		return nil
	})

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
			// This should never happen, since we only add nodes with valid addresses.
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
	schema.Hook(hook)

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
		arch, err := osarch.ArchitectureGetLocalID()
		if err != nil {
			return false, err
		}
		err = query.Transaction(db, func(tx *sql.Tx) error {
			stmt := `
INSERT INTO nodes(id, name, address, schema, api_extensions, arch) VALUES(1, 'none', '0.0.0.0', ?, ?, ?)
`
			_, err = tx.Exec(stmt, SchemaVersion, apiExtensions, arch)
			if err != nil {
				return err
			}

			// Default project
			stmt = `
INSERT INTO projects (name, description) VALUES ('default', 'Default LXD project');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.images', 'true');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.profiles', 'true');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.storage.volumes', 'true');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.networks', 'true');
`
			_, err = tx.Exec(stmt)
			if err != nil {
				return err
			}

			// Default profile
			stmt = `
INSERT INTO profiles (name, description, project_id) VALUES ('default', 'Default LXD profile', 1)
`
			_, err = tx.Exec(stmt)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return false, err
		}
	}

	return true, err
}

// Generate a new name for the dqlite driver registration. We need it to be
// unique for testing, see below.
func dqliteDriverName() string {
	defer atomic.AddUint64(&dqliteDriverSerial, 1)
	return fmt.Sprintf("dqlite-%d", dqliteDriverSerial)
}

// Monotonic serial number for registering new instances of dqlite.Driver
// using the database/sql stdlib package. This is needed since there's no way
// to unregister drivers, and in unit tests more than one driver gets
// registered.
var dqliteDriverSerial uint64

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
			panic("unexpected return value from compareVersions")
		}
	}
	return nil
}

var errSomeNodesAreBehind = fmt.Errorf("some nodes are behind this node's version")
