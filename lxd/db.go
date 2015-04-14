package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

const DB_CURRENT_VERSION int = 5

var (
	DbErrAlreadyDefined = fmt.Errorf("already exists")
)

func createDb(p string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %s\n", err)
	}
	stmt := `
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    UNIQUE (name)
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, key)
);
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id),
    UNIQUE (container_device_id, key)
);
CREATE TABLE containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id),
    FOREIGN KEY (profile_id) REFERENCES profiles(id)
);
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id),
    UNIQUE (name)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id)
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id)
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id)
);
CREATE TABLE profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id)
);
CREATE TABLE schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`

	_, err = db.Exec(stmt, DB_CURRENT_VERSION)
	if err != nil {
		return db, err
	}

	err = createDefaultProfile(db)

	return db, err
}

func getSchema(db *sql.DB) (int, error) {
	rows, err := db.Query("SELECT max(version) FROM schema")
	if err != nil {
		return 0, nil
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		rows.Scan(&v)
		return v, nil
	}
	return 0, nil
}

func updateFromV4(db *sql.DB) error {
	stmt := `
CREATE TABLE config (
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
	old_password := ""
	if err == nil {
		defer passOut.Close()
		buff := make([]byte, 96)
		_, err = passOut.Read(buff)
		if err != nil {
			return err
		}

		old_password = hex.EncodeToString(buff)
		stmt := `INSERT INTO config (key, value) VALUES ("core.trust_password", ?);`

		_, err := db.Exec(stmt, old_password)
		if err != nil {
			return err
		}

		return os.Remove(passfname)
	}

	return nil
}

func updateFromV3(db *sql.DB) error {
	err := createDefaultProfile(db)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`, 4)
	return err
}

func updateFromV2(db *sql.DB) error {
	stmt := `
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id),
    UNIQUE (container_device_id, key)
);
CREATE TABLE containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id),
    FOREIGN KEY (profile_id) REFERENCES profiles(id)
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id)
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id)
);
CREATE TABLE profiles_devices_config (
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
func updateFromV1(db *sql.DB) error {
	// v1..v2 adds images aliases
	stmt := `
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id),
    UNIQUE (name)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 2)
	return err
}

func updateFromV0(db *sql.DB) error {
	// v0..v1 adds schema table
	stmt := `
CREATE TABLE schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 1)
	return err
}

func updateDb(db *sql.DB, prev_version int) error {
	if prev_version < 0 || prev_version > DB_CURRENT_VERSION {
		return fmt.Errorf("Bad database version: %d\n", prev_version)
	}
	if prev_version == DB_CURRENT_VERSION {
		return nil
	}
	var err error
	if prev_version < 1 {
		err = updateFromV0(db)
		if err != nil {
			return err
		}
	}
	if prev_version < 2 {
		err = updateFromV1(db)
		if err != nil {
			return err
		}
	}
	if prev_version < 3 {
		err = updateFromV2(db)
		if err != nil {
			return err
		}
	}
	if prev_version < 4 {
		err = updateFromV3(db)
		if err != nil {
			return err
		}
	}
	if prev_version < 5 {
		err = updateFromV4(db)
		if err != nil {
			return err
		}
	}
	return nil
}

func createDefaultProfile(db *sql.DB) error {
	rows, err := shared.DbQuery(db, "SELECT id FROM profiles WHERE name=?", "default")
	if err != nil {
		return err
	}
	defer rows.Close()
	id := -1
	for rows.Next() {
		var xId int
		rows.Scan(&xId)
		id = xId
	}
	if id != -1 {
		// default profile already exists
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	result, err := tx.Exec("INSERT INTO profiles (name) VALUES (?)", "default")
	if err != nil {
		tx.Rollback()
		return err
	}
	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return err
	}
	id = int(id64)

	result, err = tx.Exec(`INSERT INTO profiles_devices 
		(profile_id, name, type) VALUES (?, ?, ?)`,
		id, "eth0", "nic")
	if err != nil {
		tx.Rollback()
		return err
	}
	id64, err = result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return err
	}
	devId := int(id64)

	_, err = tx.Exec(`INSERT INTO profiles_devices_config
		(profile_device_id, key, value) VALUES (?, ?, ?)`,
		devId, "nictype", "bridged")
	if err != nil {
		tx.Rollback()
		return err
	}

	/* TODO - analyze system to choose a bridge */
	_, err = tx.Exec(`INSERT INTO profiles_devices_config
		(profile_device_id, key, value) VALUES (?, ?, ?)`,
		devId, "parent", "lxcbr0")
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func initDb(d *Daemon) error {
	dbpath := shared.VarPath("lxd.db")
	var db *sql.DB
	var err error
	timeout := 30 // TODO - make this command-line configurable?
	openPath := fmt.Sprintf("%s?_busy_timeout=%d&_txlock=immediate", dbpath, timeout*1000)
	if !shared.PathExists(dbpath) {
		db, err = createDb(openPath)
		if err != nil {
			return err
		}
	} else {
		db, err = sql.Open("sqlite3", openPath)
		if err != nil {
			return err
		}
	}

	d.db = db

	v, err := getSchema(db)
	if err != nil {
		return fmt.Errorf("Bad database, or database too new for this lxd version")
	}

	if v != DB_CURRENT_VERSION {
		err = updateDb(db, v)
		if err != nil {
			return err
		}
	}

	return nil
}

var NoSuchImageError = errors.New("No such image")

func dbImageGet(d *Daemon, name string, public bool) (*shared.ImageBaseInfo, error) {

	// count potential images first, if more than one
	// return error
	var countImg int
	var err error
	q := "SELECT count(id) FROM images WHERE fingerprint like ?"
	if public {
		q = q + " AND public=1"
	}

	arg1 := []interface{}{name + "%"}
	arg2 := []interface{}{&countImg}
	err = shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err != nil {
		return nil, err
	}

	if countImg > 1 {
		return nil, fmt.Errorf("Multiple images for fingerprint")
	}

	image := new(shared.ImageBaseInfo)

	var create, expire, upload *time.Time
	q = `SELECT id, fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date FROM images WHERE fingerprint like ?`
	if public {
		q = q + " AND public=1"
	}

	arg2 = []interface{}{&image.Id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Public, &image.Architecture,
		&create, &expire, &upload}

	err = shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err != nil {
		return nil, err
	}

	if create != nil {
		t := *create
		image.CreationDate = t.Unix()
	} else {
		image.CreationDate = 0
	}
	if expire != nil {
		t := *expire
		image.ExpiryDate = t.Unix()
	} else {
		image.ExpiryDate = 0
	}
	t := *upload
	image.UploadDate = t.Unix()

	switch {
	case err == sql.ErrNoRows:
		return nil, NoSuchImageError
	case err != nil:
		return nil, err
	default:
		return image, nil
	}

}

func dbImageGetById(d *Daemon, id int) (string, error) {
	q := "SELECT fingerprint FROM images WHERE id=?"
	var fp string
	arg1 := []interface{}{id}
	arg2 := []interface{}{&fp}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return "", NoSuchImageError
	}
	if err != nil {
		return "", err
	}

	return fp, nil
}

func dbAliasGet(d *Daemon, name string) (int, int, error) {
	q := "SELECT id, image_id FROM images_aliases WHERE name=?"
	var id int
	var imageid int
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &imageid}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return 0, 0, NoSuchImageError
	}
	if err != nil {
		return 0, 0, err
	}
	return id, imageid, nil
}

func dbAddAlias(d *Daemon, name string, tgt int, desc string) error {
	stmt := `INSERT into images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := shared.DbExec(d.db, stmt, name, tgt, desc)
	return err
}

func dbGetConfig(d *Daemon, c *lxdContainer) (map[string]string, error) {
	q := `SELECT key, value FROM containers_config WHERE container_id=?`
	rows, err := shared.DbQuery(d.db, q, c.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	config := map[string]string{}

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		config[key] = value
	}

	return config, nil
}

func dbGetProfileConfig(d *Daemon, name string) (map[string]string, error) {
	q := "SELECT id FROM profiles WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("Profile %s not found", name)
	}
	if err != nil {
		return nil, err
	}

	q = `SELECT key, value FROM profiles_config JOIN profiles
		ON profiles_config.profile_id=profiles.id
		WHERE name=?`
	rows, err := shared.DbQuery(d.db, q, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	config := map[string]string{}

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		config[key] = value
	}

	return config, nil
}

type Profile struct {
	name  string
	order int
}
type Profiles []Profile

func dbGetProfiles(d *Daemon, c *lxdContainer) ([]string, error) {
	q := `SELECT name FROM containers_profiles JOIN profiles
		ON containers_profiles.profile_id=profiles.id
		WHERE container_id=? ORDER BY containers_profiles.apply_order`
	rows, err := shared.DbQuery(d.db, q, c.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []string

	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, name)
	}

	return profiles, nil
}

func dbGetDevices(d *Daemon, qName string, isprofile bool) (shared.Devices, error) {
	var q, q2 string
	if isprofile {
		q = `SELECT profiles_devices.id, profiles_devices.name, profiles_devices.type
			FROM profiles_devices JOIN profiles
			ON profiles_devices.profile_id = profiles.id
			WHERE profiles.name=?`
		q2 = `SELECT key, value FROM profiles_devices_config WHERE profile_device_id=?`
	} else {
		q = `SELECT containers_devices.id, containers_devices.name, containers_devices.type
			FROM containers_devices JOIN containers
			ON containers_devices.container_id = containers.id
			WHERE containers.name=?`
		q2 = `SELECT key, value FROM containers_devices_config WHERE container_device_id=?`
	}
	rows, err := shared.DbQuery(d.db, q, qName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	devices := shared.Devices{}

	for rows.Next() {
		var id int
		var name, dtype string
		if err := rows.Scan(&id, &name, &dtype); err != nil {
			return nil, err
		}
		newdev := shared.Device{}
		rows2, err := shared.DbQuery(d.db, q2, id)
		if err != nil {
			return nil, err
		}
		defer rows2.Close()

		newdev["type"] = dtype
		for rows2.Next() {
			var k, v string
			rows2.Scan(&k, &v)
			if !shared.ValidDeviceConfig(dtype, k, v) {
				return nil, fmt.Errorf("Invalid config for device type %s: %s = %s\n", dtype, k, v)
			}
			newdev[k] = v
		}

		devices[name] = newdev
	}

	return devices, nil
}
