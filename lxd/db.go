package main

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

var (
	// DbErrAlreadyDefined hapens when the given entry already exists,
	// for example a container.
	DbErrAlreadyDefined = fmt.Errorf("already exists")

	/* NoSuchObjectError is in the case of joins (and probably other) queries,
	 * we don't get back sql.ErrNoRows when no rows are returned, even though we do
	 * on selects without joins. Instead, you can use this error to
	 * propagate up and generate proper 404s to the client when something
	 * isn't found so we don't abuse sql.ErrNoRows any more than we
	 * already do.
	 */
	NoSuchObjectError = fmt.Errorf("No such object")
)

// Profile is here to order Profiles.
type Profile struct {
	name  string
	order int
}

// Profiles will contain a list of all Profiles.
type Profiles []Profile

const DB_CURRENT_VERSION int = 9

// CURRENT_SCHEMA contains the current SQLite SQL Schema.
const CURRENT_SCHEMA string = `
CREATE TABLE IF NOT EXISTS certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE IF NOT EXISTS config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE IF NOT EXISTS containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    power_state INTEGER NOT NULL DEFAULT 0,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
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
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id) ON DELETE CASCADE,
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
CREATE TABLE IF NOT EXISTS images (
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
CREATE TABLE IF NOT EXISTS images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
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
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);`

// Create the initial (current) schema for a given SQLite DB connection.
// This should stay indempotent.
func createDb(db *sql.DB) (err error) {
	_, err = db.Exec(CURRENT_SCHEMA)
	if err != nil {
		return err
	}

	// To make the schema creation indempotent, only insert the schema version
	// if there isn't one already.
	latestVersion := getSchema(db)

	if latestVersion == 0 {
		// There isn't an entry for schema version, let's put it in.
		insertStmt := `INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
		_, err = db.Exec(insertStmt, DB_CURRENT_VERSION)
		if err != nil {
			return err
		}
	}

	err = createDefaultProfile(db)

	return err
}

func getSchema(db *sql.DB) (v int) {
	arg1 := []interface{}{}
	arg2 := []interface{}{&v}
	q := "SELECT max(version) FROM schema"
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return 0
	}
	return v
}

func updateFromV8(db *sql.DB) error {
	stmt := `
UPDATE certificates SET fingerprint = replace(fingerprint, " ", "");
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 9)
	return err
}

func updateFromV7(db *sql.DB) error {
	stmt := `
UPDATE config SET key='core.trust_password' WHERE key IN ('password', 'trust_password', 'trust-password', 'core.trust-password');
DELETE FROM config WHERE key != 'core.trust_password';
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 8)
	return err
}

func updateFromV6(db *sql.DB) error {
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

func updateFromV5(db *sql.DB) error {
	stmt := `
ALTER TABLE containers ADD COLUMN power_state INTEGER NOT NULL DEFAULT 0;
ALTER TABLE containers ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, 6)
	return err
}

func updateFromV4(db *sql.DB) error {
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
func updateFromV1(db *sql.DB) error {
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

func updateFromV0(db *sql.DB) error {
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

func updateDb(db *sql.DB, prevVersion int) error {
	if prevVersion < 0 || prevVersion > DB_CURRENT_VERSION {
		return fmt.Errorf("Bad database version: %d\n", prevVersion)
	}
	if prevVersion == DB_CURRENT_VERSION {
		return nil
	}
	var err error
	if prevVersion < 1 {
		err = updateFromV0(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 2 {
		err = updateFromV1(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 3 {
		err = updateFromV2(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 4 {
		err = updateFromV3(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 5 {
		err = updateFromV4(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 6 {
		err = updateFromV5(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 7 {
		err = updateFromV6(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 8 {
		err = updateFromV7(db)
		if err != nil {
			return err
		}
	}
	if prevVersion < 9 {
		err = updateFromV8(db)
		if err != nil {
			return err
		}
	}

	return nil
}

func createDefaultProfile(db *sql.DB) error {
	rows, err := dbQuery(db, "SELECT id FROM profiles WHERE name=?", "default")
	if err != nil {
		return err
	}
	defer rows.Close()
	id := -1
	for rows.Next() {
		var xID int
		rows.Scan(&xID)
		id = xID
	}
	if id != -1 {
		// default profile already exists
		return nil
	}

	tx, err := dbBegin(db)
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
	devID := int(id64)

	_, err = tx.Exec(`INSERT INTO profiles_devices_config
		(profile_device_id, key, value) VALUES (?, ?, ?)`,
		devID, "nictype", "bridged")
	if err != nil {
		tx.Rollback()
		return err
	}

	/* TODO - analyze system to choose a bridge */
	_, err = tx.Exec(`INSERT INTO profiles_devices_config
		(profile_device_id, key, value) VALUES (?, ?, ?)`,
		devID, "parent", "lxcbr0")
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// Create a database connection object and return it.
func initializeDbObject(path string) (db *sql.DB, err error) {
	var openPath string

	timeout := 5 // TODO - make this command-line configurable?

	// These are used to tune the transaction BEGIN behavior instead of using the
	// similar "locking_mode" pragma (locking for the whole database connection).
	openPath = fmt.Sprintf("%s?_busy_timeout=%d&_txlock=exclusive", path, timeout*1000)

	// Open the database. If the file doesn't exist it is created.
	db, err = sql.Open("sqlite3", openPath)
	if err != nil {
		return nil, err
	}

	// Table creation is indempotent, run it every time
	err = createDb(db)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %s\n", err)
	}

	// Run PRAGMA statements now since they are *per-connection*.
	db.Exec("PRAGMA foreign_keys=ON;") // This allows us to use ON DELETE CASCADE

	v := getSchema(db)

	if v != DB_CURRENT_VERSION {
		err = updateDb(db, v)
		if err != nil {
			return nil, err
		}
	}

	return db, nil
}

// Initialize a database connection and set it on the daemon.
func initDb(d *Daemon) (err error) {
	path := shared.VarPath("lxd.db")
	d.db, err = initializeDbObject(path)
	return err
}

func dbPasswordGet(db *sql.DB) (pwd string, err error) {
	q := "SELECT value FROM config WHERE key=\"core.trust_password\""
	value := ""
	argIn := []interface{}{}
	argOut := []interface{}{&value}
	err = dbQueryRowScan(db, q, argIn, argOut)

	if err != nil || value == "" {
		return "", fmt.Errorf("No password is set")
	}

	return value, nil
}

// dbCertInfo is here to pass the certificates content
// from the database around
type dbCertInfo struct {
	ID          int
	Fingerprint string
	Type        int
	Name        string
	Certificate string
}

// dbCertsGet returns all certificates from the DB as CertBaseInfo objects.
func dbCertsGet(db *sql.DB) (certs []*dbCertInfo, err error) {
	rows, err := dbQuery(
		db,
		"SELECT id, fingerprint, type, name, certificate FROM certificates",
	)
	if err != nil {
		return certs, err
	}

	defer rows.Close()

	for rows.Next() {
		cert := new(dbCertInfo)
		rows.Scan(
			&cert.ID,
			&cert.Fingerprint,
			&cert.Type,
			&cert.Name,
			&cert.Certificate,
		)
		certs = append(certs, cert)
	}

	return certs, nil
}

// dbCertGet gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbCertGet(db *sql.DB, fingerprint string) (cert *dbCertInfo, err error) {
	cert = new(dbCertInfo)

	inargs := []interface{}{fingerprint + "%"}
	outfmt := []interface{}{
		&cert.ID,
		&cert.Fingerprint,
		&cert.Type,
		&cert.Name,
		&cert.Certificate,
	}

	query := `
		SELECT
			id, fingerprint, type, name, certificate
		FROM
			certificates
		WHERE fingerprint LIKE ?`

	if err = dbQueryRowScan(db, query, inargs, outfmt); err != nil {
		return nil, err
	}

	return cert, err
}

// dbCertSave stores a CertBaseInfo object in the db,
// it will ignore the ID field from the dbCertInfo.
func dbCertSave(db *sql.DB, cert *dbCertInfo) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
			INSERT INTO certificates (
				fingerprint,
				type,
				name,
				certificate
			) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(
		cert.Fingerprint,
		cert.Type,
		cert.Name,
		cert.Certificate,
	)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

// dbCertDelete deletes a certificate from the db.
func dbCertDelete(db *sql.DB, fingerprint string) error {
	_, err := dbExec(
		db,
		"DELETE FROM certificates WHERE fingerprint=?",
		fingerprint,
	)

	return err
}

// dbImageGet gets an ImageBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbImageGet(db *sql.DB, fingerprint string, public bool) (*shared.ImageBaseInfo, error) {
	var err error
	var create, expire, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := new(shared.ImageBaseInfo)

	// These two humongous things will be filled by the call to DbQueryRowScan
	inargs := []interface{}{fingerprint + "%"}
	outfmt := []interface{}{&image.Id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Public, &image.Architecture,
		&create, &expire, &upload}

	query := `
        SELECT
            id, fingerprint, filename, size, public, architecture,
            creation_date, expiry_date, upload_date
        FROM
            images
        WHERE fingerprint like ?`

	if public {
		query = query + " AND public=1"
	}

	err = dbQueryRowScan(db, query, inargs, outfmt)

	if err != nil {
		return nil, err // Likely: there are no rows for this fingerprint
	}

	// Some of the dates can be nil in the DB, let's process them.
	if create != nil {
		image.CreationDate = create.Unix()
	} else {
		image.CreationDate = 0
	}
	if expire != nil {
		image.ExpiryDate = expire.Unix()
	} else {
		image.ExpiryDate = 0
	}
	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadDate = upload.Unix()

	return image, nil
}

// Get an image's fingerprint for a given alias name.
func dbAliasGet(db *sql.DB, name string) (fingerprint string, err error) {
	q := `
        SELECT
            fingerprint
        FROM images AS i JOIN images_aliases AS a
        ON a.image_id == i.id
        WHERE name=?`

	inargs := []interface{}{name}
	outfmt := []interface{}{&fingerprint}

	err = dbQueryRowScan(db, q, inargs, outfmt)

	if err == sql.ErrNoRows {
		return "", NoSuchObjectError
	}
	if err != nil {
		return "", err
	}
	return fingerprint, nil
}

// Insert an alias into the database.
func dbAddAlias(db *sql.DB, name string, imageID int, desc string) error {
	stmt := `INSERT into images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := dbExec(db, stmt, name, imageID, desc)
	return err
}

// Get the container configuration map from the DB
func dbGetConfig(db *sql.DB, containerID int) (map[string]string, error) {
	var key, value string
	q := `SELECT key, value FROM containers_config WHERE container_id=?`

	inargs := []interface{}{containerID}
	outfmt := []interface{}{key, value}

	// Results is already a slice here, not db Rows anymore.
	results, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return nil, err //SmartError will wrap this and make "not found" errors pretty
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

// Get the profile configuration map from the DB
func dbGetProfileConfig(db *sql.DB, name string) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM profiles_config
        JOIN profiles ON profiles_config.profile_id=profiles.id
		WHERE name=?`
	inargs := []interface{}{name}
	outfmt := []interface{}{key, value}
	results, err := dbQueryScan(db, query, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		/*
		 * If we didn't get any rows here, let's check to make sure the
		 * profile really exists; if it doesn't, let's send back a 404.
		 */
		query := "SELECT id FROM profiles WHERE name=?"
		var id int
		results, err := dbQueryScan(db, query, []interface{}{name}, []interface{}{id})
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			return nil, NoSuchObjectError
		}
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

// Get a list of profiles for a given container id.
func dbGetProfiles(db *sql.DB, containerID int) ([]string, error) {
	var name string
	var profiles []string

	query := `
        SELECT name FROM containers_profiles
        JOIN profiles ON containers_profiles.profile_id=profiles.id
		WHERE container_id=?
        ORDER BY containers_profiles.apply_order`
	inargs := []interface{}{containerID}
	outfmt := []interface{}{name}

	results, err := dbQueryScan(db, query, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range results {
		name = r[0].(string)

		profiles = append(profiles, name)
	}

	return profiles, nil
}

func dbGetDeviceConfig(db *sql.DB, id int, isprofile bool) (shared.Device, error) {
	var query string
	var key, value string
	newdev := shared.Device{} // That's a map[string]string
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}

	if isprofile {
		query = `SELECT key, value FROM profiles_devices_config WHERE profile_device_id=?`
	} else {
		query = `SELECT key, value FROM containers_devices_config WHERE container_device_id=?`
	}

	results, err := dbQueryScan(db, query, inargs, outfmt)

	if err != nil {
		return newdev, err
	}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)
		newdev[key] = value
	}

	return newdev, nil
}

func dbGetDevices(db *sql.DB, qName string, isprofile bool) (shared.Devices, error) {
	var q string
	if isprofile {
		q = `SELECT profiles_devices.id, profiles_devices.name, profiles_devices.type
			FROM profiles_devices JOIN profiles
			ON profiles_devices.profile_id = profiles.id
			WHERE profiles.name=?`
	} else {
		q = `SELECT containers_devices.id, containers_devices.name, containers_devices.type
			FROM containers_devices JOIN containers
			ON containers_devices.container_id = containers.id
			WHERE containers.name=?`
	}
	var id int
	var name, dtype string
	inargs := []interface{}{qName}
	outfmt := []interface{}{id, name, dtype}
	results, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	devices := shared.Devices{}
	for _, r := range results {
		id = r[0].(int)
		name = r[1].(string)
		dtype = r[2].(string)
		newdev, err := dbGetDeviceConfig(db, id, isprofile)
		if err != nil {
			return nil, err
		}
		newdev["type"] = dtype
		devices[name] = newdev
	}

	return devices, nil
}

func dbListContainers(d *Daemon) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? ORDER BY name")
	inargs := []interface{}{cTypeRegular}
	var container string
	outfmt := []interface{}{container}
	result, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, container := range result {
		ret = append(ret, container[0].(string))
	}

	return ret, nil
}

func isDbLockedError(err error) bool {
	if err == nil {
		return false
	}
	if err == sqlite3.ErrLocked || err == sqlite3.ErrBusy {
		return true
	}
	if err.Error() == "database is locked" {
		return true
	}
	return false
}

func dbBegin(db *sql.DB) (*sql.Tx, error) {
	for {
		tx, err := db.Begin()
		if err == nil {
			return tx, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbBegin: error %q\n", err)
			return nil, err
		}
		shared.Debugf("DbBegin: DB was locked\n")
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func txCommit(tx *sql.Tx) error {
	for {
		err := tx.Commit()
		if err == nil {
			return nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("Txcommit: error %q\n", err)
			return err
		}
		shared.Debugf("Txcommit: db was locked\n")
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func dbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	for {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbQuery: query %q error %q\n", q, err)
			return err
		}
		shared.Debugf("DbQueryRowScan: query %q args %q, DB was locked\n", q, args)
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func dbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	for {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbQuery: query %q error %q\n", q, err)
			return nil, err
		}
		shared.Debugf("DbQuery: query %q args %q, DB was locked\n", q, args)
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func doDbQueryScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) ([][]interface{}, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return [][]interface{}{}, err
	}
	defer rows.Close()
	result := [][]interface{}{}
	for rows.Next() {
		ptrargs := make([]interface{}, len(outargs))
		for i := range outargs {
			switch t := outargs[i].(type) {
			case string:
				str := ""
				ptrargs[i] = &str
			case int:
				integer := 0
				ptrargs[i] = &integer
			default:
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s\n", t)
			}
		}
		err = rows.Scan(ptrargs...)
		if err != nil {
			return [][]interface{}{}, err
		}
		newargs := make([]interface{}, len(outargs))
		for i := range ptrargs {
			switch t := outargs[i].(type) {
			case string:
				newargs[i] = *ptrargs[i].(*string)
			case int:
				newargs[i] = *ptrargs[i].(*int)
			default:
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s\n", t)
			}
		}
		result = append(result, newargs)
	}
	err = rows.Err()
	if err != nil {
		return [][]interface{}{}, err
	}
	return result, nil
}

/*
 * . q is the database query
 * . inargs is an array of interfaces containing the query arguments
 * . outfmt is an array of interfaces containing the right types of output
 *   arguments, i.e.
 *      var arg1 string
 *      var arg2 int
 *      outfmt := {}interface{}{arg1, arg2}
 *
 * The result will be an array (one per output row) of arrays (one per output argument)
 * of interfaces, containing pointers to the actual output arguments.
 */
func dbQueryScan(db *sql.DB, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
	for {
		result, err := doDbQueryScan(db, q, inargs, outfmt)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbQuery: query %q error %q\n", q, err)
			return nil, err
		}
		shared.Debugf("DbQueryscan: query %q inargs %q, DB was locked\n", q, inargs)
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func dbExec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
	for {
		result, err := db.Exec(q, args...)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbExec: query %q error %q\n", q, err)
			return nil, err
		}
		shared.Debugf("DbExec: query %q args %q, DB was locked\n", q, args)
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}
