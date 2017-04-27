package main

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* Database updates are one-time actions that are needed to move an
   existing database from one version of the schema to the next.

   Those updates are applied at startup time before anything else in LXD
   is initialized. This means that they should be entirely
   self-contained and not touch anything but the database.

   Calling LXD functions isn't allowed as such functions may themselves
   depend on a newer DB schema and so would fail when upgrading a very old
   version of LXD.

   DO NOT USE this mechanism for one-time actions which do not involve
   changes to the database schema. Use patches instead.

   Only append to the updates list, never remove entries and never re-order them.
*/

var dbUpdates = []dbUpdate{
	{version: 1, run: dbUpdateFromV0},
	{version: 2, run: dbUpdateFromV1},
	{version: 3, run: dbUpdateFromV2},
	{version: 4, run: dbUpdateFromV3},
	{version: 5, run: dbUpdateFromV4},
	{version: 6, run: dbUpdateFromV5},
	{version: 7, run: dbUpdateFromV6},
	{version: 8, run: dbUpdateFromV7},
	{version: 9, run: dbUpdateFromV8},
	{version: 10, run: dbUpdateFromV9},
	{version: 11, run: dbUpdateFromV10},
	{version: 12, run: dbUpdateFromV11},
	{version: 13, run: dbUpdateFromV12},
	{version: 14, run: dbUpdateFromV13},
	{version: 15, run: dbUpdateFromV14},
	{version: 16, run: dbUpdateFromV15},
	{version: 17, run: dbUpdateFromV16},
	{version: 18, run: dbUpdateFromV17},
	{version: 19, run: dbUpdateFromV18},
	{version: 20, run: dbUpdateFromV19},
	{version: 21, run: dbUpdateFromV20},
	{version: 22, run: dbUpdateFromV21},
	{version: 23, run: dbUpdateFromV22},
	{version: 24, run: dbUpdateFromV23},
	{version: 25, run: dbUpdateFromV24},
	{version: 26, run: dbUpdateFromV25},
	{version: 27, run: dbUpdateFromV26},
	{version: 28, run: dbUpdateFromV27},
	{version: 29, run: dbUpdateFromV28},
	{version: 30, run: dbUpdateFromV29},
	{version: 31, run: dbUpdateFromV30},
	{version: 32, run: dbUpdateFromV31},
	{version: 33, run: dbUpdateFromV32},
	{version: 34, run: dbUpdateFromV33},
	{version: 35, run: dbUpdateFromV34},
	{version: 36, run: dbUpdateFromV35},
}

type dbUpdate struct {
	version int
	run     func(previousVersion int, version int, d *Daemon) error
}

func (u *dbUpdate) apply(currentVersion int, d *Daemon) error {
	// Get the current schema version

	logger.Debugf("Updating DB schema from %d to %d", currentVersion, u.version)

	err := u.run(currentVersion, u.version, d)
	if err != nil {
		return err
	}

	_, err = d.db.Exec("INSERT INTO schema (version, updated_at) VALUES (?, strftime(\"%s\"));", u.version)
	if err != nil {
		return err
	}

	return nil
}

func dbUpdatesApplyAll(d *Daemon) error {
	currentVersion := dbGetSchema(d.db)

	backup := false
	for _, update := range dbUpdates {
		if update.version <= currentVersion {
			continue
		}

		if !d.MockMode && !backup {
			logger.Infof("Updating the LXD database schema. Backup made as \"lxd.db.bak\"")
			err := shared.FileCopy(shared.VarPath("lxd.db"), shared.VarPath("lxd.db.bak"))
			if err != nil {
				return err
			}

			backup = true
		}

		err := update.apply(currentVersion, d)
		if err != nil {
			return err
		}

		currentVersion = update.version
	}

	return nil
}

// Schema updates begin here
func dbUpdateFromV35(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("ALTER TABLE networks ADD COLUMN description TEXT;")
	return err
}

func dbUpdateFromV34(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE IF NOT EXISTS storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    driver VARCHAR(255) NOT NULL,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    UNIQUE (storage_pool_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV33(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE IF NOT EXISTS networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (network_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV32(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("ALTER TABLE containers ADD COLUMN last_use_date DATETIME;")
	return err
}

func dbUpdateFromV31(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE IF NOT EXISTS patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV30(currentVersion int, version int, d *Daemon) error {
	if d.MockMode {
		return nil
	}

	entries, err := ioutil.ReadDir(shared.VarPath("containers"))
	if err != nil {
		/* If the directory didn't exist before, the user had never
		 * started containers, so we don't need to fix up permissions
		 * on anything.
		 */
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !shared.IsDir(shared.VarPath("containers", entry.Name(), "rootfs")) {
			continue
		}

		info, err := os.Stat(shared.VarPath("containers", entry.Name(), "rootfs"))
		if err != nil {
			return err
		}

		if int(info.Sys().(*syscall.Stat_t).Uid) == 0 {
			err := os.Chmod(shared.VarPath("containers", entry.Name()), 0700)
			if err != nil {
				return err
			}

			err = os.Chown(shared.VarPath("containers", entry.Name()), 0, 0)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func dbUpdateFromV29(currentVersion int, version int, d *Daemon) error {
	if d.MockMode {
		return nil
	}

	if shared.PathExists(shared.VarPath("zfs.img")) {
		err := os.Chmod(shared.VarPath("zfs.img"), 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

func dbUpdateFromV28(currentVersion int, version int, d *Daemon) error {
	stmt := `
INSERT INTO profiles_devices (profile_id, name, type) SELECT id, "aadisable", 2 FROM profiles WHERE name="docker";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "source", "/dev/null" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker" AND profiles_devices.name = "aadisable";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "path", "/sys/module/apparmor/parameters/enabled" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker" AND profiles_devices.name = "aadisable";`
	d.db.Exec(stmt)

	return nil
}

func dbUpdateFromV27(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("UPDATE profiles_devices SET type=3 WHERE type='unix-char';")
	return err
}

func dbUpdateFromV26(currentVersion int, version int, d *Daemon) error {
	stmt := `
ALTER TABLE images ADD COLUMN auto_update INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias VARCHAR(255) NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV25(currentVersion int, version int, d *Daemon) error {
	stmt := `
INSERT INTO profiles (name, description) VALUES ("docker", "Profile supporting docker in containers");
INSERT INTO profiles_config (profile_id, key, value) SELECT id, "security.nesting", "true" FROM profiles WHERE name="docker";
INSERT INTO profiles_config (profile_id, key, value) SELECT id, "linux.kernel_modules", "overlay, nf_nat" FROM profiles WHERE name="docker";
INSERT INTO profiles_devices (profile_id, name, type) SELECT id, "fuse", "unix-char" FROM profiles WHERE name="docker";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "path", "/dev/fuse" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker";`
	d.db.Exec(stmt)

	return nil
}

func dbUpdateFromV24(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("ALTER TABLE containers ADD COLUMN stateful INTEGER NOT NULL DEFAULT 0;")
	return err
}

func dbUpdateFromV23(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("ALTER TABLE profiles ADD COLUMN description TEXT;")
	return err
}

func dbUpdateFromV22(currentVersion int, version int, d *Daemon) error {
	stmt := `
DELETE FROM containers_devices_config WHERE key='type';
DELETE FROM profiles_devices_config WHERE key='type';`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV21(currentVersion int, version int, d *Daemon) error {
	_, err := d.db.Exec("ALTER TABLE containers ADD COLUMN creation_date DATETIME NOT NULL DEFAULT 0;")
	return err
}

func dbUpdateFromV20(currentVersion int, version int, d *Daemon) error {
	stmt := `
UPDATE containers_devices SET name='__lxd_upgrade_root' WHERE name='root';
UPDATE profiles_devices SET name='__lxd_upgrade_root' WHERE name='root';

INSERT INTO containers_devices (container_id, name, type) SELECT id, "root", 2 FROM containers;
INSERT INTO containers_devices_config (container_device_id, key, value) SELECT id, "path", "/" FROM containers_devices WHERE name='root';`
	_, err := d.db.Exec(stmt)

	return err
}

func dbUpdateFromV19(currentVersion int, version int, d *Daemon) error {
	stmt := `
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices_config WHERE container_device_id NOT IN (SELECT id FROM containers_devices WHERE container_id IN (SELECT id FROM containers));
DELETE FROM containers_devices WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_profiles WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM images_aliases WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_properties WHERE image_id NOT IN (SELECT id FROM images);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV18(currentVersion int, version int, d *Daemon) error {
	var id int
	var value string

	// Update container config
	rows, err := dbQueryScan(d.db, "SELECT id, value FROM containers_config WHERE key='limits.memory'", nil, []interface{}{id, value})
	if err != nil {
		return err
	}

	for _, row := range rows {
		id = row[0].(int)
		value = row[1].(string)

		// If already an integer, don't touch
		_, err := strconv.Atoi(value)
		if err == nil {
			continue
		}

		// Generate the new value
		value = strings.ToUpper(value)
		value += "B"

		// Deal with completely broken values
		_, err = shared.ParseByteSizeString(value)
		if err != nil {
			logger.Debugf("Invalid container memory limit, id=%d value=%s, removing.", id, value)
			_, err = d.db.Exec("DELETE FROM containers_config WHERE id=?;", id)
			if err != nil {
				return err
			}
		}

		// Set the new value
		_, err = d.db.Exec("UPDATE containers_config SET value=? WHERE id=?", value, id)
		if err != nil {
			return err
		}
	}

	// Update profiles config
	rows, err = dbQueryScan(d.db, "SELECT id, value FROM profiles_config WHERE key='limits.memory'", nil, []interface{}{id, value})
	if err != nil {
		return err
	}

	for _, row := range rows {
		id = row[0].(int)
		value = row[1].(string)

		// If already an integer, don't touch
		_, err := strconv.Atoi(value)
		if err == nil {
			continue
		}

		// Generate the new value
		value = strings.ToUpper(value)
		value += "B"

		// Deal with completely broken values
		_, err = shared.ParseByteSizeString(value)
		if err != nil {
			logger.Debugf("Invalid profile memory limit, id=%d value=%s, removing.", id, value)
			_, err = d.db.Exec("DELETE FROM profiles_config WHERE id=?;", id)
			if err != nil {
				return err
			}
		}

		// Set the new value
		_, err = d.db.Exec("UPDATE profiles_config SET value=? WHERE id=?", value, id)
		if err != nil {
			return err
		}
	}

	return nil
}

func dbUpdateFromV17(currentVersion int, version int, d *Daemon) error {
	stmt := `
DELETE FROM profiles_config WHERE key LIKE 'volatile.%';
UPDATE containers_config SET key='limits.cpu' WHERE key='limits.cpus';
UPDATE profiles_config SET key='limits.cpu' WHERE key='limits.cpus';`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV16(currentVersion int, version int, d *Daemon) error {
	stmt := `
UPDATE config SET key='storage.lvm_vg_name' WHERE key = 'core.lvm_vg_name';
UPDATE config SET key='storage.lvm_thinpool_name' WHERE key = 'core.lvm_thinpool_name';`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV15(currentVersion int, version int, d *Daemon) error {
	// munge all LVM-backed containers' LV names to match what is
	// required for snapshot support

	cNames, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	err = daemonConfigInit(d.db)
	if err != nil {
		return err
	}

	vgName := daemonConfig["storage.lvm_vg_name"].Get()

	for _, cName := range cNames {
		var lvLinkPath string
		if strings.Contains(cName, shared.SnapshotDelimiter) {
			lvLinkPath = shared.VarPath("snapshots", fmt.Sprintf("%s.lv", cName))
		} else {
			lvLinkPath = shared.VarPath("containers", fmt.Sprintf("%s.lv", cName))
		}

		if !shared.PathExists(lvLinkPath) {
			continue
		}

		newLVName := strings.Replace(cName, "-", "--", -1)
		newLVName = strings.Replace(newLVName, shared.SnapshotDelimiter, "-", -1)

		if cName == newLVName {
			logger.Debug("No need to rename, skipping", log.Ctx{"cName": cName, "newLVName": newLVName})
			continue
		}

		logger.Debug("About to rename cName in lv upgrade", log.Ctx{"lvLinkPath": lvLinkPath, "cName": cName, "newLVName": newLVName})

		output, err := shared.RunCommand("lvrename", vgName, cName, newLVName)
		if err != nil {
			return fmt.Errorf("Could not rename LV '%s' to '%s': %v\noutput:%s", cName, newLVName, err, output)
		}

		if err := os.Remove(lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't remove lvLinkPath '%s'", lvLinkPath)
		}
		newLinkDest := fmt.Sprintf("/dev/%s/%s", vgName, newLVName)
		if err := os.Symlink(newLinkDest, lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't recreate symlink '%s'->'%s'", lvLinkPath, newLinkDest)
		}
	}

	return nil
}

func dbUpdateFromV14(currentVersion int, version int, d *Daemon) error {
	stmt := `
PRAGMA foreign_keys=OFF; -- So that integrity doesn't get in the way for now

DELETE FROM containers_config WHERE key="volatile.last_state.power";

INSERT INTO containers_config (container_id, key, value)
    SELECT id, "volatile.last_state.power", "RUNNING"
    FROM containers WHERE power_state=1;

INSERT INTO containers_config (container_id, key, value)
    SELECT id, "volatile.last_state.power", "STOPPED"
    FROM containers WHERE power_state != 1;

CREATE TABLE tmp (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);

INSERT INTO tmp SELECT id, name, architecture, type, ephemeral FROM containers;

DROP TABLE containers;
ALTER TABLE tmp RENAME TO containers;

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV13(currentVersion int, version int, d *Daemon) error {
	stmt := `
UPDATE containers_config SET key='volatile.base_image' WHERE key = 'volatile.baseImage';`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV12(currentVersion int, version int, d *Daemon) error {
	stmt := `
ALTER TABLE images ADD COLUMN cached INTEGER NOT NULL DEFAULT 0;
ALTER TABLE images ADD COLUMN last_use_date DATETIME;`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV11(currentVersion int, version int, d *Daemon) error {
	if d.MockMode {
		// No need to move snapshots no mock runs,
		// dbUpdateFromV12 will then set the db version to 13
		return nil
	}

	cNames, err := dbContainersList(d.db, cTypeSnapshot)
	if err != nil {
		return err
	}

	errors := 0

	for _, cName := range cNames {
		snapParentName, snapOnlyName, _ := containerGetParentAndSnapshotName(cName)
		oldPath := shared.VarPath("containers", snapParentName, "snapshots", snapOnlyName)
		newPath := shared.VarPath("snapshots", snapParentName, snapOnlyName)
		if shared.PathExists(oldPath) && !shared.PathExists(newPath) {
			logger.Info(
				"Moving snapshot",
				log.Ctx{
					"snapshot": cName,
					"oldPath":  oldPath,
					"newPath":  newPath})

			// Rsync
			// containers/<container>/snapshots/<snap0>
			// to
			// snapshots/<container>/<snap0>
			output, err := rsyncLocalCopy(oldPath, newPath, "")
			if err != nil {
				logger.Error(
					"Failed rsync snapshot",
					log.Ctx{
						"snapshot": cName,
						"output":   string(output),
						"err":      err})
				errors++
				continue
			}

			// Remove containers/<container>/snapshots/<snap0>
			if err := os.RemoveAll(oldPath); err != nil {
				logger.Error(
					"Failed to remove the old snapshot path",
					log.Ctx{
						"snapshot": cName,
						"oldPath":  oldPath,
						"err":      err})

				// Ignore this error.
				// errors++
				// continue
			}

			// Remove /var/lib/lxd/containers/<container>/snapshots
			// if its empty.
			cPathParent := filepath.Dir(oldPath)
			if ok, _ := shared.PathIsEmpty(cPathParent); ok {
				os.Remove(cPathParent)
			}

		} // if shared.PathExists(oldPath) && !shared.PathExists(newPath) {
	} // for _, cName := range cNames {

	// Refuse to start lxd if a rsync failed.
	if errors > 0 {
		return fmt.Errorf("Got errors while moving snapshots, see the log output.")
	}

	return nil
}

func dbUpdateFromV10(currentVersion int, version int, d *Daemon) error {
	if d.MockMode {
		// No need to move lxc to containers in mock runs,
		// dbUpdateFromV12 will then set the db version to 13
		return nil
	}

	if shared.PathExists(shared.VarPath("lxc")) {
		err := os.Rename(shared.VarPath("lxc"), shared.VarPath("containers"))
		if err != nil {
			return err
		}

		logger.Debugf("Restarting all the containers following directory rename")
		containersShutdown(d)
		containersRestart(d)
	}

	return nil
}

func dbUpdateFromV9(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE tmp (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(255) NOT NULL default "none",
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);

INSERT INTO tmp SELECT * FROM containers_devices;

UPDATE containers_devices SET type=0 WHERE id IN (SELECT id FROM tmp WHERE type="none");
UPDATE containers_devices SET type=1 WHERE id IN (SELECT id FROM tmp WHERE type="nic");
UPDATE containers_devices SET type=2 WHERE id IN (SELECT id FROM tmp WHERE type="disk");
UPDATE containers_devices SET type=3 WHERE id IN (SELECT id FROM tmp WHERE type="unix-char");
UPDATE containers_devices SET type=4 WHERE id IN (SELECT id FROM tmp WHERE type="unix-block");

DROP TABLE tmp;

CREATE TABLE tmp (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(255) NOT NULL default "none",
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE,
    UNIQUE (profile_id, name)
);

INSERT INTO tmp SELECT * FROM profiles_devices;

UPDATE profiles_devices SET type=0 WHERE id IN (SELECT id FROM tmp WHERE type="none");
UPDATE profiles_devices SET type=1 WHERE id IN (SELECT id FROM tmp WHERE type="nic");
UPDATE profiles_devices SET type=2 WHERE id IN (SELECT id FROM tmp WHERE type="disk");
UPDATE profiles_devices SET type=3 WHERE id IN (SELECT id FROM tmp WHERE type="unix-char");
UPDATE profiles_devices SET type=4 WHERE id IN (SELECT id FROM tmp WHERE type="unix-block");

DROP TABLE tmp;`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV8(currentVersion int, version int, d *Daemon) error {
	stmt := `
UPDATE certificates SET fingerprint = replace(fingerprint, " ", "");`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV7(currentVersion int, version int, d *Daemon) error {
	stmt := `
UPDATE config SET key='core.trust_password' WHERE key IN ('password', 'trust_password', 'trust-password', 'core.trust-password');
DELETE FROM config WHERE key != 'core.trust_password';`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV6(currentVersion int, version int, d *Daemon) error {
	// This update recreates the schemas that need an ON DELETE CASCADE foreign
	// key.
	stmt := `
PRAGMA foreign_keys=OFF; -- So that integrity doesn't get in the way for now

CREATE TEMP TABLE tmp AS SELECT * FROM containers_config;
DROP TABLE containers_config;
CREATE TABLE IF NOT EXISTS containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
INSERT INTO containers_config SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM containers_devices;
DROP TABLE containers_devices;
CREATE TABLE IF NOT EXISTS containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
INSERT INTO containers_devices SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM containers_devices_config;
DROP TABLE containers_devices_config;
CREATE TABLE IF NOT EXISTS containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id) ON DELETE CASCADE,
    UNIQUE (container_device_id, key)
);
INSERT INTO containers_devices_config SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM containers_profiles;
DROP TABLE containers_profiles;
CREATE TABLE IF NOT EXISTS containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
INSERT INTO containers_profiles SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM images_aliases;
DROP TABLE images_aliases;
CREATE TABLE IF NOT EXISTS images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
INSERT INTO images_aliases SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM images_properties;
DROP TABLE images_properties;
CREATE TABLE IF NOT EXISTS images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
INSERT INTO images_properties SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM profiles_config;
DROP TABLE profiles_config;
CREATE TABLE IF NOT EXISTS profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
INSERT INTO profiles_config SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM profiles_devices;
DROP TABLE profiles_devices;
CREATE TABLE IF NOT EXISTS profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
INSERT INTO profiles_devices SELECT * FROM tmp;
DROP TABLE tmp;

CREATE TEMP TABLE tmp AS SELECT * FROM profiles_devices_config;
DROP TABLE profiles_devices_config;
CREATE TABLE IF NOT EXISTS profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
INSERT INTO profiles_devices_config SELECT * FROM tmp;
DROP TABLE tmp;

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.`
	_, err := d.db.Exec(stmt)
	if err != nil {
		return err
	}

	// Get the rows with broken foreign keys an nuke them
	rows, err := d.db.Query("PRAGMA foreign_key_check;")
	if err != nil {
		return err
	}

	var tablestodelete []string
	var rowidtodelete []int

	defer rows.Close()
	for rows.Next() {
		var tablename string
		var rowid int
		var targetname string
		var keynumber int

		rows.Scan(&tablename, &rowid, &targetname, &keynumber)
		tablestodelete = append(tablestodelete, tablename)
		rowidtodelete = append(rowidtodelete, rowid)
	}
	rows.Close()

	for i := range tablestodelete {
		_, err = d.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE rowid = %d;", tablestodelete[i], rowidtodelete[i]))
		if err != nil {
			return err
		}
	}

	return err
}

func dbUpdateFromV5(currentVersion int, version int, d *Daemon) error {
	stmt := `
ALTER TABLE containers ADD COLUMN power_state INTEGER NOT NULL DEFAULT 0;
ALTER TABLE containers ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV4(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE IF NOT EXISTS config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);`

	_, err := d.db.Exec(stmt)
	if err != nil {
		return err
	}

	passfname := shared.VarPath("adminpwd")
	passOut, err := os.Open(passfname)
	oldPassword := ""
	if err == nil {
		defer passOut.Close()
		buff := make([]byte, 96)
		_, err = passOut.Read(buff)
		if err != nil {
			return err
		}

		oldPassword = hex.EncodeToString(buff)
		stmt := `INSERT INTO config (key, value) VALUES ("core.trust_password", ?);`

		_, err := d.db.Exec(stmt, oldPassword)
		if err != nil {
			return err
		}

		return os.Remove(passfname)
	}

	return nil
}

func dbUpdateFromV3(currentVersion int, version int, d *Daemon) error {
	// Attempt to create a default profile (but don't fail if already there)
	d.db.Exec("INSERT INTO profiles (name) VALUES (\"default\");")

	return nil
}

func dbUpdateFromV2(currentVersion int, version int, d *Daemon) error {
	stmt := `
CREATE TABLE IF NOT EXISTS containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
CREATE TABLE IF NOT EXISTS containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id),
    UNIQUE (container_device_id, key)
);
CREATE TABLE IF NOT EXISTS containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id)
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV1(currentVersion int, version int, d *Daemon) error {
	// v1..v2 adds images aliases
	stmt := `
CREATE TABLE IF NOT EXISTS images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);`
	_, err := d.db.Exec(stmt)
	return err
}

func dbUpdateFromV0(currentVersion int, version int, d *Daemon) error {
	// v0..v1 adds schema table
	stmt := `
CREATE TABLE IF NOT EXISTS schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);`
	_, err := d.db.Exec(stmt)
	return err
}
