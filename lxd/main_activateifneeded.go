package main

import (
	"database/sql"
	"fmt"
	"os"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

func init() {
	sql.Register("dqlite_direct_access", &sqlite3.SQLiteDriver{ConnectHook: sqliteDirectAccess})
}

type cmdActivateifneeded struct {
	global *cmdGlobal
}

func (c *cmdActivateifneeded) Command() *cobra.Command {
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
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdActivateifneeded) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Don't start a full daemon, we just need database access
	d := defaultDaemon()

	// Check if either the local database or the legacy local database
	// files exists.
	path := d.os.LocalDatabasePath()
	if !shared.PathExists(d.os.LocalDatabasePath()) {
		path = d.os.LegacyLocalDatabasePath()
		if !shared.PathExists(path) {
			logger.Debugf("No local database, so no need to start the daemon now")
			return nil
		}
	}

	// Open the database directly to avoid triggering any initialization
	// code, in particular the data migration from node to cluster db.
	sqldb, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	d.db = db.ForLegacyPatches(sqldb)

	// Load the configured address from the database
	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return err
	}

	// Look for network socket
	if address != "" {
		logger.Debugf("Daemon has core.https_address set, activating...")
		_, err := lxd.ConnectLXDUnix("", nil)
		return err
	}

	// Load the idmap for unprivileged instances
	d.os.IdmapSet, err = idmap.DefaultIdmapSet("", "")
	if err != nil {
		return err
	}

	// Look for auto-started or previously started instances
	path = d.os.GlobalDatabasePath()
	if !shared.PathExists(path) {
		path = d.os.LegacyGlobalDatabasePath()
		if !shared.PathExists(path) {
			logger.Debugf("No global database, so no need to start the daemon now")
			return nil
		}
	}
	sqldb, err = sql.Open("dqlite_direct_access", path+"?mode=ro")
	if err != nil {
		return err
	}
	defer sqldb.Close()

	d.cluster, err = db.ForLocalInspectionWithPreparedStmts(sqldb)
	if err != nil {
		return err
	}

	instances, err := instance.LoadNodeAll(d.State(), instancetype.Any)
	if err != nil {
		return err
	}

	for _, inst := range instances {
		config := inst.ExpandedConfig()
		lastState := config["volatile.last_state.power"]
		autoStart := config["boot.autostart"]

		if inst.IsRunning() {
			logger.Debugf("Daemon has running instances, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}

		if lastState == "RUNNING" || lastState == "Running" || shared.IsTrue(autoStart) {
			logger.Debugf("Daemon has auto-started instances, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}

		// Check for scheduled instance snapshots
		if config["snapshots.schedule"] != "" {
			logger.Debugf("Daemon has scheduled instance snapshots, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}
	}

	// Check for scheduled volume snapshots
	var volumes []db.StorageVolumeArgs
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		volumes, err = tx.GetStoragePoolVolumesWithType(db.StoragePoolVolumeTypeCustom)
		return err
	})
	if err != nil {
		return err
	}

	for _, vol := range volumes {
		if vol.Config["snapshots.schedule"] != "" {
			logger.Debugf("Daemon has scheduled volume snapshots, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}
	}

	logger.Debugf("No need to start the daemon now")
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
