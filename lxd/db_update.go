package main

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

func dbUpdateFromV20(d *Daemon) error {
	cNames, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	for _, name := range cNames {
		c, err := containerLoadByName(d, name)
		if err != nil {
			return err
		}

		rootfs := false
		for _, m := range c.ExpandedDevices() {
			if m["type"] == "disk" && m["path"] == "/" {
				rootfs = true
				break
			}
		}

		if !rootfs {
			deviceName := "root"
			for {
				if c.ExpandedDevices()[deviceName] == nil {
					break
				}

				deviceName += "_"
			}

			newDevices := c.LocalDevices()
			newDevices[deviceName] = shared.Device{"type": "disk", "path": "/"}

			updateArgs := containerArgs{
				Architecture: c.Architecture(),
				Config:       c.LocalConfig(),
				Devices:      newDevices,
				Ephemeral:    c.IsEphemeral(),
				Profiles:     c.Profiles(),
			}

			err = c.Update(updateArgs, false)
			if err != nil {
				return err
			}
		}
	}

	stmt := `
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err = d.db.Exec(stmt, 21)
	return err
}

func dbUpdateFromV19(db *sql.DB) error {
	stmt := `
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices_config WHERE container_device_id NOT IN (SELECT id FROM containers_devices WHERE container_id IN (SELECT id FROM containers));
DELETE FROM containers_devices WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_profiles WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM images_aliases WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_properties WHERE image_id NOT IN (SELECT id FROM images);
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 20)
	return err
}

func dbUpdateFromV18(db *sql.DB) error {
	var id int
	var value string

	// Update container config
	rows, err := dbQueryScan(db, "SELECT id, value FROM containers_config WHERE key='limits.memory'", nil, []interface{}{id, value})
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
		_, err = deviceParseBytes(value)
		if err != nil {
			shared.Debugf("Invalid container memory limit, id=%d value=%s, removing.", id, value)
			_, err = db.Exec("DELETE FROM containers_config WHERE id=?;", id)
			if err != nil {
				return err
			}
		}

		// Set the new value
		_, err = db.Exec("UPDATE containers_config SET value=? WHERE id=?", value, id)
		if err != nil {
			return err
		}
	}

	// Update profiles config
	rows, err = dbQueryScan(db, "SELECT id, value FROM profiles_config WHERE key='limits.memory'", nil, []interface{}{id, value})
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
		_, err = deviceParseBytes(value)
		if err != nil {
			shared.Debugf("Invalid profile memory limit, id=%d value=%s, removing.", id, value)
			_, err = db.Exec("DELETE FROM profiles_config WHERE id=?;", id)
			if err != nil {
				return err
			}
		}

		// Set the new value
		_, err = db.Exec("UPDATE profiles_config SET value=? WHERE id=?", value, id)
		if err != nil {
			return err
		}
	}

	_, err = db.Exec("INSERT INTO schema (version, updated_at) VALUES (?, strftime(\"%s\"));", 19)
	return err
}

func dbUpdateFromV17(db *sql.DB) error {
	stmt := `
DELETE FROM profiles_config WHERE key LIKE 'volatile.%';
UPDATE containers_config SET key='limits.cpu' WHERE key='limits.cpus';
UPDATE profiles_config SET key='limits.cpu' WHERE key='limits.cpus';
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 18)
	return err
}

func dbUpdateFromV16(db *sql.DB) error {
	stmt := `
UPDATE config SET key='storage.lvm_vg_name' WHERE key = 'core.lvm_vg_name';
UPDATE config SET key='storage.lvm_thinpool_name' WHERE key = 'core.lvm_thinpool_name';
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 17)
	return err
}

func dbUpdateFromV15(d *Daemon) error {
	// munge all LVM-backed containers' LV names to match what is
	// required for snapshot support

	cNames, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	vgName, err := d.ConfigValueGet("storage.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error checking server config: %v", err)
	}

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
			shared.Log.Debug("No need to rename, skipping", log.Ctx{"cName": cName, "newLVName": newLVName})
			continue
		}

		shared.Log.Debug("About to rename cName in lv upgrade", log.Ctx{"lvLinkPath": lvLinkPath, "cName": cName, "newLVName": newLVName})

		output, err := exec.Command("lvrename", vgName, cName, newLVName).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Could not rename LV '%s' to '%s': %v\noutput:%s", cName, newLVName, err, string(output))
		}

		if err := os.Remove(lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't remove lvLinkPath '%s'", lvLinkPath)
		}
		newLinkDest := fmt.Sprintf("/dev/%s/%s", vgName, newLVName)
		if err := os.Symlink(newLinkDest, lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't recreate symlink '%s'->'%s'", lvLinkPath, newLinkDest)
		}
	}
	stmt := `
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err = d.db.Exec(stmt, 16)
	return err
}

func dbUpdateFromV14(db *sql.DB) error {
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

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 15)
	return err
}

func dbUpdateFromV13(db *sql.DB) error {
	stmt := `
UPDATE containers_config SET key='volatile.base_image' WHERE key = 'volatile.baseImage';
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 14)
	return err
}

func dbUpdateFromV12(db *sql.DB) error {
	stmt := `
ALTER TABLE images ADD COLUMN cached INTEGER NOT NULL DEFAULT 0;
ALTER TABLE images ADD COLUMN last_use_date DATETIME;
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 13)
	return err
}

func dbUpdateFromV11(d *Daemon) error {
	if d.IsMock {
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
		snappieces := strings.SplitN(cName, shared.SnapshotDelimiter, 2)
		oldPath := shared.VarPath("containers", snappieces[0], "snapshots", snappieces[1])
		newPath := shared.VarPath("snapshots", snappieces[0], snappieces[1])
		if shared.PathExists(oldPath) && !shared.PathExists(newPath) {
			shared.Log.Info(
				"Moving snapshot",
				log.Ctx{
					"snapshot": cName,
					"oldPath":  oldPath,
					"newPath":  newPath})

			// Rsync
			// containers/<container>/snapshots/<snap0>
			//   to
			// snapshots/<container>/<snap0>
			output, err := storageRsyncCopy(oldPath, newPath)
			if err != nil {
				shared.Log.Error(
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
				shared.Log.Error(
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

	stmt := `
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err = d.db.Exec(stmt, 12)

	return err
}

func dbUpdateFromV10(d *Daemon) error {
	if d.IsMock {
		// No need to move lxc to containers in mock runs,
		// dbUpdateFromV12 will then set the db version to 13
		return nil
	}

	if shared.PathExists(shared.VarPath("lxc")) {
		err := os.Rename(shared.VarPath("lxc"), shared.VarPath("containers"))
		if err != nil {
			return err
		}

		shared.Debugf("Restarting all the containers following directory rename")
		containersShutdown(d)
		containersRestart(d)
	}

	stmt := `
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := d.db.Exec(stmt, 11)
	return err
}

func dbUpdateFromV9(db *sql.DB) error {
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

DROP TABLE tmp;
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 10)
	return err
}

func dbUpdateFromV8(db *sql.DB) error {
	stmt := `
UPDATE certificates SET fingerprint = replace(fingerprint, " ", "");
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 9)
	return err
}

func dbUpdateFromV7(db *sql.DB) error {
	stmt := `
UPDATE config SET key='core.trust_password' WHERE key IN ('password', 'trust_password', 'trust-password', 'core.trust-password');
DELETE FROM config WHERE key != 'core.trust_password';
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 8)
	return err
}

func dbUpdateFromV6(db *sql.DB) error {
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

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 7)
	if err != nil {
		return err
	}

	// Get the rows with broken foreign keys an nuke them
	rows, err := db.Query("PRAGMA foreign_key_check;")
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
		_, err = db.Exec(fmt.Sprintf("DELETE FROM %s WHERE rowid = %d;", tablestodelete[i], rowidtodelete[i]))
		if err != nil {
			return err
		}
	}

	return err
}

func dbUpdateFromV5(db *sql.DB) error {
	stmt := `
ALTER TABLE containers ADD COLUMN power_state INTEGER NOT NULL DEFAULT 0;
ALTER TABLE containers ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 6)
	return err
}

func dbUpdateFromV4(db *sql.DB) error {
	stmt := `
CREATE TABLE IF NOT EXISTS config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 5)
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

		_, err := db.Exec(stmt, oldPassword)
		if err != nil {
			return err
		}

		return os.Remove(passfname)
	}

	return nil
}

func dbUpdateFromV3(db *sql.DB) error {
	err := dbProfileCreateDefault(db)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`, 4)
	return err
}

func dbUpdateFromV2(db *sql.DB) error {
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
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 3)
	return err
}

/* Yeah we can do htis in a more clever way */
func dbUpdateFromV1(db *sql.DB) error {
	// v1..v2 adds images aliases
	stmt := `
CREATE TABLE IF NOT EXISTS images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 2)
	return err
}

func dbUpdateFromV0(db *sql.DB) error {
	// v0..v1 adds schema table
	stmt := `
CREATE TABLE IF NOT EXISTS schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 1)
	return err
}

func dbUpdate(d *Daemon, prevVersion int) error {
	db := d.db

	if prevVersion < 0 || prevVersion > DB_CURRENT_VERSION {
		return fmt.Errorf("Bad database version: %d", prevVersion)
	}
	if prevVersion == DB_CURRENT_VERSION {
		return nil
	}
	var err error
	if prevVersion < 1 {
		err = dbUpdateFromV0(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 2 {
		err = dbUpdateFromV1(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 3 {
		err = dbUpdateFromV2(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 4 {
		err = dbUpdateFromV3(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 5 {
		err = dbUpdateFromV4(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 6 {
		err = dbUpdateFromV5(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 7 {
		err = dbUpdateFromV6(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 8 {
		err = dbUpdateFromV7(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 9 {
		err = dbUpdateFromV8(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 10 {
		err = dbUpdateFromV9(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 11 {
		err = dbUpdateFromV10(d)
		if err != nil {
			return err
		}
	}
	if prevVersion < 12 {
		err = dbUpdateFromV11(d)
		if err != nil {
			return err
		}
	}
	if prevVersion < 13 {
		err = dbUpdateFromV12(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 14 {
		err = dbUpdateFromV13(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 15 {
		err = dbUpdateFromV14(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 16 {
		err = dbUpdateFromV15(d)
		if err != nil {
			return err
		}
	}
	if prevVersion < 17 {
		err = dbUpdateFromV16(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 18 {
		err = dbUpdateFromV17(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 19 {
		err = dbUpdateFromV18(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 20 {
		err = dbUpdateFromV19(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 21 {
		err = dbUpdateFromV20(d)
		if err != nil {
			return err
		}
	}

	return nil
}
