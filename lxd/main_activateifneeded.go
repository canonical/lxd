package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

func init() {
	sql.Register("dqlite_direct_access", &sqlite3.SQLiteDriver{ConnectHook: sqliteDirectAccess})
}

type cmdActivateifneeded struct {
	global *cmdGlobal
}

func (c *cmdActivateifneeded) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "activateifneeded"
	cmd.Short = "Check if LXD should be started"
	cmd.Long = `Description:
  Check if LXD should be started

  This command will check if LXD has any auto-started instances,
  instances which were running prior to LXD's last shutdown or if it's
  configured to listen on the network address.

  If at least one of those is true, then a connection will be attempted to the
  LXD socket which will cause a socket-activated LXD to be spawned.
`
	cmd.RunE = c.run

	return cmd
}

func (c *cmdActivateifneeded) run(cmd *cobra.Command, args []string) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	// Don't start a full daemon, we just need database access
	d := defaultDaemon()

	// Check if either the local database files exists.
	path := d.os.LocalDatabasePath()
	if !shared.PathExists(d.os.LocalDatabasePath()) {
		logger.Debug("No local database, so no need to start the daemon now")
		return nil
	}

	// Open the database directly to avoid triggering any initialization
	// code, in particular the data migration from node to cluster db.
	sqldb, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}

	d.db.Node = db.DirectAccess(sqldb)

	// Load the configured address from the database
	err = d.db.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		d.localConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}

	localHTTPAddress := d.localConfig.HTTPSAddress()

	startLXD := func() error {
		d, err := lxd.ConnectLXDUnix("", &lxd.ConnectionArgs{
			SkipGetServer: true, // Don't hit the /1.0 endpoint to avoid unnecessary work on server.
		})
		if err != nil {
			return err
		}

		// Request / endpoint to make a minimum request sufficient to start LXD.
		_, _, err = d.RawQuery(http.MethodGet, "/", nil, "")
		return err
	}

	// Look for network socket
	if localHTTPAddress != "" {
		logger.Debug("Daemon has core.https_address set, activating...")
		return startLXD()
	}

	// Load the idmap for unprivileged instances
	d.os.IdmapSet, err = idmap.DefaultIdmapSet("", "")
	if err != nil {
		return err
	}

	// Look for auto-started or previously started instances
	path = d.os.GlobalDatabasePath()
	if !shared.PathExists(path) {
		logger.Debug("No global database, so no need to start the daemon now")
		return nil
	}

	sqldb, err = sql.Open("dqlite_direct_access", path+"?mode=ro")
	if err != nil {
		return err
	}

	defer func() { _ = sqldb.Close() }()

	d.db.Cluster, err = db.ForLocalInspectionWithPreparedStmts(sqldb)
	if err != nil {
		return err
	}

	// Don't call d.State() as that will run instance driver feature checks (amongst other unnecessary things).
	// We just need access to the DBs.
	s := &state.State{
		DB:          d.db,
		LocalConfig: d.localConfig,
	}

	// Because we've not loaded the cluster member name from the global DB, this function will load all
	// instances in the database and not filter by local member name. However as we only do this when
	// core.https_address is unset (as there is an early check above) this is not inefficient because we will
	// only get to this part for standalone servers.
	instances, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		return err
	}

	for _, inst := range instances {
		if instanceShouldAutoStart(inst) {
			logger.Debug("Daemon has auto-started instances, activating...")
			return startLXD()
		}

		config := inst.ExpandedConfig()

		if config["volatile.last_state.power"] == instance.PowerStateRunning {
			logger.Debug("Daemon has running instances, activating...")
			return startLXD()
		}

		// Check for scheduled instance snapshots
		if config["snapshots.schedule"] != "" {
			logger.Debug("Daemon has scheduled instance snapshots, activating...")
			return startLXD()
		}
	}

	// Check for scheduled volume snapshots
	var volumes []db.StorageVolumeArgs
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		volumes, err = tx.GetStoragePoolVolumesWithType(ctx, cluster.StoragePoolVolumeTypeCustom, false)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, vol := range volumes {
		if vol.Config["snapshots.schedule"] != "" {
			logger.Debug("Daemon has scheduled volume snapshots, activating...")
			return startLXD()
		}
	}

	logger.Debug("No need to start the daemon now")
	return nil
}

// Configure the sqlite connection so that it's safe to access the
// dqlite-managed sqlite file, also without setting up raft.
func sqliteDirectAccess(conn *sqlite3.SQLiteConn) error {
	// Ensure journal mode is set to WAL, as this is a requirement for
	// replication.
	_, err := conn.Exec("PRAGMA journal_mode=wal", nil)
	if err != nil {
		return err
	}

	// Ensure we don't truncate or checkpoint the WAL on exit, as this
	// would bork replication which must be in full control of the WAL
	// file.
	_, err = conn.Exec("PRAGMA journal_size_limit=-1", nil)
	if err != nil {
		return err
	}

	// Ensure WAL autocheckpoint is disabled, since checkpoints are
	// triggered explicitly by dqlite.
	_, err = conn.Exec("PRAGMA wal_autocheckpoint=0", nil)
	if err != nil {
		return err
	}

	return nil
}
