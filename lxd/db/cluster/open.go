package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/canonical/go-dqlite/v3/driver"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/version"
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
		return nil, fmt.Errorf("Failed to create dqlite driver: %w", err)
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
		return nil, fmt.Errorf("cannot open cluster database: %w", err)
	}

	return db, nil
}

// EnsureSchema applies all relevant schema updates to the cluster database.
//
// Before actually doing anything, this function will make sure that all members in the cluster have a schema
// version and a number of API extensions that match our one.
// If it's not the case an api.StatusError with code http.StatusPreconditionFailed is returned,
// this indicates that this member should wait for them to become aligned.
func EnsureSchema(db *sql.DB, address string, dir string, serverUUID string) error {
	apiExtensions := version.APIExtensionsCount()

	backupDone := false
	hook := func(ctx context.Context, version int, tx *sql.Tx) error {
		// Check if this is a fresh instance.
		isUpdate, err := schema.DoesSchemaTableExist(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to check if schema table exists: %w", err)
		}

		if !isUpdate {
			return nil
		}

		// Check if we're clustered
		clustered := true
		n, err := selectUnclusteredNodesCount(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch standalone member count: %w", err)
		}

		if n > 1 {
			// This should never happen, since we only add cluster members with valid addresses.
			return errors.New("Found more than one cluster member with a standalone address (0.0.0.0)")
		} else if n == 1 {
			clustered = false
		}

		// If we're not clustered, backup the local cluster database directory
		// before performing any schema change. This makes sense only in the
		// non-clustered case, because otherwise the directory would be
		// re-populated by replication.
		if !clustered && !backupDone {
			logger.Info("Updating the LXD global schema. Backup made as \"global.bak\"")
			err := shared.DirCopy(
				filepath.Join(dir, "global"),
				filepath.Join(dir, "global.bak"),
			)
			if err != nil {
				return fmt.Errorf("Failed to backup global database: %w", err)
			}

			backupDone = true
		}

		if version == -1 {
			logger.Debug("Running pre-update queries from file for global DB schema")
		} else {
			logger.Debugf("Updating global DB schema from %d to %d", version, version+1)
		}

		return nil
	}

	check := func(ctx context.Context, current int, tx *sql.Tx) error {
		// If we're bootstrapping a fresh schema, skip any check, since
		// it's safe to assume we are the only node.
		if current == 0 {
			return nil
		}

		// Check if we're clustered
		n, err := selectUnclusteredNodesCount(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch standalone member count: %w", err)
		}

		if n > 1 {
			// This should never happen, since we only add nodes with valid addresses.
			return errors.New("Found more than one cluster member with a standalone address (0.0.0.0)")
		} else if n == 1 {
			address = "0.0.0.0" // We're not clustered
		}

		// Update the schema and api_extension columns of ourselves.
		err = updateNodeVersion(tx, address, apiExtensions)
		if err != nil {
			return fmt.Errorf("Failed to update cluster member version info for %q: %w", address, err)
		}

		return checkClusterIsUpgradable(ctx, tx, [2]int{len(updates), apiExtensions})
	}

	schema := Schema()
	schema.File(filepath.Join(dir, "patch.global.sql")) // Optional custom queries
	schema.Check(check)
	schema.Hook(hook)

	var initial int
	err := query.Retry(context.TODO(), func(ctx context.Context) error {
		var err error
		initial, err = schema.Ensure(db)
		if err != nil {
			return fmt.Errorf("Failed to ensure schema: %w", err)
		}

		err = query.Transaction(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
			return applyTriggers(ctx, tx)
		})
		if err != nil {
			return fmt.Errorf("Failed to apply triggers: %w", err)
		}

		return err
	})
	if err != nil {
		return err
	}

	// When creating a database from scratch, insert an entry for node
	// 1. This is needed for referential integrity with other tables. Also,
	// create a default profile.
	if initial == 0 {
		arch, err := osarch.ArchitectureGetLocalID()
		if err != nil {
			return err
		}

		err = query.Transaction(context.TODO(), db, func(_ context.Context, tx *sql.Tx) error {
			stmt := `
INSERT INTO nodes(id, name, address, schema, api_extensions, arch, description) VALUES(1, 'none', '0.0.0.0', ?, ?, ?, '')
`
			_, err = tx.Exec(stmt, SchemaVersion, apiExtensions, arch)
			if err != nil {
				return err
			}

			// If bootstrapping, set the cluster-wide UUID to the value of the initial server UUID.
			_, err = tx.Exec(`INSERT INTO config (key, value) VALUES ('volatile.uuid', ?)`, serverUUID)
			if err != nil {
				return err
			}

			// Default project
			var defaultProjectStmt strings.Builder
			_, _ = defaultProjectStmt.WriteString("INSERT INTO projects (name, description) VALUES ('default', 'Default LXD project');")

			// Enable all features for default project.
			for featureName := range ProjectFeatures {
				_, _ = defaultProjectStmt.WriteString(fmt.Sprintf("INSERT INTO projects_config (project_id, key, value) VALUES (1, '%s', 'true');", featureName))
			}

			_, err = tx.Exec(defaultProjectStmt.String())
			if err != nil {
				return err
			}

			// Server administrators auth group
			stmt = `INSERT INTO auth_groups (name, description) VALUES ('admins', 'Server administrators')`
			_, err = tx.Exec(stmt)
			if err != nil {
				return err
			}

			stmt = `INSERT INTO auth_groups_permissions (auth_group_id, entity_type, entity_id, entitlement) VALUES (1, ?, 0, ?)`
			_, err = tx.Exec(stmt, entityTypeCodeServer, string(auth.EntitlementAdmin))
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

			// Default cluster group
			stmt = `
INSERT INTO cluster_groups (name, description) VALUES ('default', 'Default cluster group');
INSERT INTO nodes_cluster_groups (node_id, group_id) VALUES(1, 1);
`
			_, err = tx.Exec(stmt)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
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

func checkClusterIsUpgradable(ctx context.Context, tx *sql.Tx, target [2]int) error {
	// Get the current versions in the nodes table.
	versions, err := selectNodesVersions(ctx, tx)
	if err != nil {
		return fmt.Errorf("Failed fetching current member versions: %w", err)
	}

	for _, version := range versions {
		n, err := util.CompareVersions(target, version)
		if err != nil {
			return err
		}

		switch n {
		case 0:
			// Versions are equal, there's hope for the update.
			// Let's check the next member.
			continue
		case 1:
			// Our version is ahead, we should wait for other members to align to our version.
			return api.StatusErrorf(http.StatusPreconditionFailed, "A cluster member's version (%v) is behind this cluster member's version (%v), please ensure versions match", version, target)
		case 2:
			// Our version is behind, we should wait for other members to align to our version.
			return api.StatusErrorf(http.StatusPreconditionFailed, "This cluster member's version (%v) is behind another member's version (%v), please ensure versions match", target, version)
		default:
			panic("Unexpected return value from compareVersions")
		}
	}
	return nil
}
