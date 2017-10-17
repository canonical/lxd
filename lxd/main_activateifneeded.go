package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

func cmdActivateIfNeeded(args *Args) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Don't start a full daemon, we just need DB access
	d := DefaultDaemon()

	if !shared.PathExists(shared.VarPath("lxd.db")) {
		logger.Debugf("No DB, so no need to start the daemon now.")
		return nil
	}

	// Open the database directly to avoid triggering any initialization
	// code, in particular the data migration from node to cluster db.
	sqldb, err := sql.Open("sqlite3", filepath.Join(d.os.VarDir, "lxd.db"))
	if err != nil {
		return err
	}
	d.db = db.ForLegacyPatches(sqldb)

	/* Load the configured address the database */
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

	// Load the idmap for unprivileged containers
	d.os.IdmapSet, err = idmap.DefaultIdmapSet("")
	if err != nil {
		return err
	}

	// Look for auto-started or previously started containers
	result, err := d.db.ContainersList(db.CTypeRegular)
	if err != nil {
		return err
	}

	for _, name := range result {
		c, err := containerLoadByName(d.State(), name)
		if err != nil {
			return err
		}

		config := c.ExpandedConfig()
		lastState := config["volatile.last_state.power"]
		autoStart := config["boot.autostart"]

		if c.IsRunning() {
			logger.Debugf("Daemon has running containers, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}

		if lastState == "RUNNING" || lastState == "Running" || shared.IsTrue(autoStart) {
			logger.Debugf("Daemon has auto-started containers, activating...")
			_, err := lxd.ConnectLXDUnix("", nil)
			return err
		}
	}

	logger.Debugf("No need to start the daemon now.")
	return nil
}
