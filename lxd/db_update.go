package main

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
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
   changes to the database schema. Use patches instead (see lxd/patches.go).

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
	run     func(previousVersion int, version int, db *sql.DB) error
}

func (u *dbUpdate) apply(currentVersion int, db *sql.DB) error {
	// Get the current schema version

	logger.Debugf("Updating DB schema from %d to %d", currentVersion, u.version)

	err := u.run(currentVersion, u.version, db)
	if err != nil {
		return err
	}

	_, err = db.Exec("INSERT INTO schema (version, updated_at) VALUES (?, strftime(\"%s\"));", u.version)
	if err != nil {
		return err
	}

	return nil
}

// Apply all possible database patches. If "doBackup" is true, the
// sqlite file will be backed up before any update is applied. If
// "postApply" it's passed, it will be called after each database
// update gets successfully applied, and be passed the its version (as
// of now "postApply" is only used by the daemon as a mean to apply
// the legacy V10 and V15 non-db updates during the database upgrade
// sequence to, avoid changing semantics see PR #3322).
func dbUpdatesApplyAll(db *sql.DB, doBackup bool, postApply func(int) error) error {
	currentVersion := dbGetSchema(db)

	backup := false
	for _, update := range dbUpdates {
		if update.version <= currentVersion {
			continue
		}

		if doBackup && !backup {
			logger.Infof("Updating the LXD database schema. Backup made as \"lxd.db.bak\"")
			err := shared.FileCopy(shared.VarPath("lxd.db"), shared.VarPath("lxd.db.bak"))
			if err != nil {
				return err
			}

			backup = true
		}

		err := update.apply(currentVersion, db)
		if err != nil {
			return err
		}
		if postApply != nil {
			err = postApply(update.version)
			if err != nil {
				return err
			}
		}

		currentVersion = update.version
	}

	return nil
}

// Schema updates begin here
func dbUpdateFromV35(currentVersion int, version int, db *sql.DB) error {
	stmts := `
CREATE TABLE tmp (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
INSERT INTO tmp (id, name, image_id, description)
    SELECT id, name, image_id, description
    FROM images_aliases;
DROP TABLE images_aliases;
ALTER TABLE tmp RENAME TO images_aliases;

ALTER TABLE networks ADD COLUMN description TEXT;
ALTER TABLE storage_pools ADD COLUMN description TEXT;
ALTER TABLE storage_volumes ADD COLUMN description TEXT;
ALTER TABLE containers ADD COLUMN description TEXT;
`
	_, err := db.Exec(stmts)
	return err
}

func dbUpdateFromV34(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV33(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV32(currentVersion int, version int, db *sql.DB) error {
	_, err := db.Exec("ALTER TABLE containers ADD COLUMN last_use_date DATETIME;")
	return err
}

func dbUpdateFromV31(currentVersion int, version int, db *sql.DB) error {
	stmt := `
CREATE TABLE IF NOT EXISTS patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV30(currentVersion int, version int, db *sql.DB) error {
	// NOTE: this database update contained daemon-level logic which
	//       was been moved to patchUpdateFromV15 in patches.go.
	return nil
}

func dbUpdateFromV29(currentVersion int, version int, db *sql.DB) error {
	if shared.PathExists(shared.VarPath("zfs.img")) {
		err := os.Chmod(shared.VarPath("zfs.img"), 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

func dbUpdateFromV28(currentVersion int, version int, db *sql.DB) error {
	stmt := `
INSERT INTO profiles_devices (profile_id, name, type) SELECT id, "aadisable", 2 FROM profiles WHERE name="docker";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "source", "/dev/null" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker" AND profiles_devices.name = "aadisable";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "path", "/sys/module/apparmor/parameters/enabled" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker" AND profiles_devices.name = "aadisable";`
	db.Exec(stmt)

	return nil
}

func dbUpdateFromV27(currentVersion int, version int, db *sql.DB) error {
	_, err := db.Exec("UPDATE profiles_devices SET type=3 WHERE type='unix-char';")
	return err
}

func dbUpdateFromV26(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV25(currentVersion int, version int, db *sql.DB) error {
	stmt := `
INSERT INTO profiles (name, description) VALUES ("docker", "Profile supporting docker in containers");
INSERT INTO profiles_config (profile_id, key, value) SELECT id, "security.nesting", "true" FROM profiles WHERE name="docker";
INSERT INTO profiles_config (profile_id, key, value) SELECT id, "linux.kernel_modules", "overlay, nf_nat" FROM profiles WHERE name="docker";
INSERT INTO profiles_devices (profile_id, name, type) SELECT id, "fuse", "unix-char" FROM profiles WHERE name="docker";
INSERT INTO profiles_devices_config (profile_device_id, key, value) SELECT profiles_devices.id, "path", "/dev/fuse" FROM profiles_devices LEFT JOIN profiles WHERE profiles_devices.profile_id = profiles.id AND profiles.name = "docker";`
	db.Exec(stmt)

	return nil
}

func dbUpdateFromV24(currentVersion int, version int, db *sql.DB) error {
	_, err := db.Exec("ALTER TABLE containers ADD COLUMN stateful INTEGER NOT NULL DEFAULT 0;")
	return err
}

func dbUpdateFromV23(currentVersion int, version int, db *sql.DB) error {
	_, err := db.Exec("ALTER TABLE profiles ADD COLUMN description TEXT;")
	return err
}

func dbUpdateFromV22(currentVersion int, version int, db *sql.DB) error {
	stmt := `
DELETE FROM containers_devices_config WHERE key='type';
DELETE FROM profiles_devices_config WHERE key='type';`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV21(currentVersion int, version int, db *sql.DB) error {
	_, err := db.Exec("ALTER TABLE containers ADD COLUMN creation_date DATETIME NOT NULL DEFAULT 0;")
	return err
}

func dbUpdateFromV20(currentVersion int, version int, db *sql.DB) error {
	stmt := `
UPDATE containers_devices SET name='__lxd_upgrade_root' WHERE name='root';
UPDATE profiles_devices SET name='__lxd_upgrade_root' WHERE name='root';

INSERT INTO containers_devices (container_id, name, type) SELECT id, "root", 2 FROM containers;
INSERT INTO containers_devices_config (container_device_id, key, value) SELECT id, "path", "/" FROM containers_devices WHERE name='root';`
	_, err := db.Exec(stmt)

	return err
}

func dbUpdateFromV19(currentVersion int, version int, db *sql.DB) error {
	stmt := `
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices_config WHERE container_device_id NOT IN (SELECT id FROM containers_devices WHERE container_id IN (SELECT id FROM containers));
DELETE FROM containers_devices WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_profiles WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM images_aliases WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_properties WHERE image_id NOT IN (SELECT id FROM images);`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV18(currentVersion int, version int, db *sql.DB) error {
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
		_, err = shared.ParseByteSizeString(value)
		if err != nil {
			logger.Debugf("Invalid container memory limit, id=%d value=%s, removing.", id, value)
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
		_, err = shared.ParseByteSizeString(value)
		if err != nil {
			logger.Debugf("Invalid profile memory limit, id=%d value=%s, removing.", id, value)
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

	return nil
}

func dbUpdateFromV17(currentVersion int, version int, db *sql.DB) error {
	stmt := `
DELETE FROM profiles_config WHERE key LIKE 'volatile.%';
UPDATE containers_config SET key='limits.cpu' WHERE key='limits.cpus';
UPDATE profiles_config SET key='limits.cpu' WHERE key='limits.cpus';`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV16(currentVersion int, version int, db *sql.DB) error {
	stmt := `
UPDATE config SET key='storage.lvm_vg_name' WHERE key = 'core.lvm_vg_name';
UPDATE config SET key='storage.lvm_thinpool_name' WHERE key = 'core.lvm_thinpool_name';`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV15(currentVersion int, version int, db *sql.DB) error {
	// NOTE: this database update contained daemon-level logic which
	//       was been moved to patchUpdateFromV15 in patches.go.
	return nil
}

func dbUpdateFromV14(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV13(currentVersion int, version int, db *sql.DB) error {
	stmt := `
UPDATE containers_config SET key='volatile.base_image' WHERE key = 'volatile.baseImage';`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV12(currentVersion int, version int, db *sql.DB) error {
	stmt := `
ALTER TABLE images ADD COLUMN cached INTEGER NOT NULL DEFAULT 0;
ALTER TABLE images ADD COLUMN last_use_date DATETIME;`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV11(currentVersion int, version int, db *sql.DB) error {
	// NOTE: this database update contained daemon-level logic which
	//       was been moved to patchUpdateFromV15 in patches.go.
	return nil
}

func dbUpdateFromV10(currentVersion int, version int, db *sql.DB) error {
	// NOTE: this database update contained daemon-level logic which
	//       was been moved to patchUpdateFromV10 in patches.go.
	return nil
}

func dbUpdateFromV9(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV8(currentVersion int, version int, db *sql.DB) error {
	stmt := `
UPDATE certificates SET fingerprint = replace(fingerprint, " ", "");`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV7(currentVersion int, version int, db *sql.DB) error {
	stmt := `
UPDATE config SET key='core.trust_password' WHERE key IN ('password', 'trust_password', 'trust-password', 'core.trust-password');
DELETE FROM config WHERE key != 'core.trust_password';`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV6(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
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

func dbUpdateFromV5(currentVersion int, version int, db *sql.DB) error {
	stmt := `
ALTER TABLE containers ADD COLUMN power_state INTEGER NOT NULL DEFAULT 0;
ALTER TABLE containers ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;`
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV4(currentVersion int, version int, db *sql.DB) error {
	stmt := `
CREATE TABLE IF NOT EXISTS config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);`

	_, err := db.Exec(stmt)
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

func dbUpdateFromV3(currentVersion int, version int, db *sql.DB) error {
	// Attempt to create a default profile (but don't fail if already there)
	db.Exec("INSERT INTO profiles (name) VALUES (\"default\");")

	return nil
}

func dbUpdateFromV2(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV1(currentVersion int, version int, db *sql.DB) error {
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
	_, err := db.Exec(stmt)
	return err
}

func dbUpdateFromV0(currentVersion int, version int, db *sql.DB) error {
	// v0..v1 adds schema table
	stmt := `
CREATE TABLE IF NOT EXISTS schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);`
	_, err := db.Exec(stmt)
	return err
}
