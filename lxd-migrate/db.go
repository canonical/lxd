package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/canonical/lxd/shared"
)

func dbRewritePoolSource(src *lxdDaemon, dst *lxdDaemon, pool string, path string) error {
	if strings.HasPrefix(src.info.Environment.ServerVersion, "2.") {
		// LXD 2.x target
		db, err := sql.Open("sqlite3", filepath.Join(dst.path, "lxd.db"))
		if err != nil {
			return err
		}

		_, err = db.Exec("UPDATE storage_pools_config SET value=? WHERE key='source' AND storage_pool_id=(SELECT id FROM storage_pools WHERE name=?);", path, pool)
		if err != nil {
			return err
		}

		err = db.Close()
		if err != nil {
			return err
		}

		return nil
	}

	// Recent LXD target
	if !shared.PathExists(filepath.Join(dst.path, "database")) {
		err := os.MkdirAll(filepath.Join(dst.path, "database"), 0700)
		if err != nil {
			return err
		}
	}

	// Get the node name
	nodeName := "none"
	if src.info.Environment.ServerClustered {
		nodeName = src.info.Environment.ServerName
	}

	// Setup the DB patch
	patchPath := filepath.Join(dst.path, "database", "patch.global.sql")
	patch, err := os.OpenFile(patchPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	_, err = patch.WriteString(fmt.Sprintf("UPDATE storage_pools_config SET value='%s' WHERE key='source' AND storage_pool_id=(SELECT id FROM storage_pools WHERE name='%s') AND node_id=(SELECT id FROM nodes WHERE name='%s');\n", path, pool, nodeName))
	if err != nil {
		return err
	}

	err = patch.Close()
	if err != nil {
		return err
	}

	return nil
}
