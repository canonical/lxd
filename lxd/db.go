package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

var (
	// DbErrAlreadyDefined hapens when the given entry already exists,
	// for example a container.
	DbErrAlreadyDefined = fmt.Errorf("The container/snapshot already exists")

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

const DB_CURRENT_VERSION int = 21

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
    cached INTEGER NOT NULL DEFAULT 0,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    last_use_date DATETIME,
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
	latestVersion := dbGetSchema(db)

	if latestVersion == 0 {
		// There isn't an entry for schema version, let's put it in.
		insertStmt := `INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
		_, err = db.Exec(insertStmt, DB_CURRENT_VERSION)
		if err != nil {
			return err
		}
	}

	return dbProfileCreateDefault(db)
}

func dbGetSchema(db *sql.DB) (v int) {
	arg1 := []interface{}{}
	arg2 := []interface{}{&v}
	q := "SELECT max(version) FROM schema"
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return 0
	}
	return v
}

// Create a database connection object and return it.
func initializeDbObject(d *Daemon, path string) (err error) {
	var openPath string

	timeout := 5 // TODO - make this command-line configurable?

	// These are used to tune the transaction BEGIN behavior instead of using the
	// similar "locking_mode" pragma (locking for the whole database connection).
	openPath = fmt.Sprintf("%s?_busy_timeout=%d&_txlock=exclusive", path, timeout*1000)

	// Open the database. If the file doesn't exist it is created.
	d.db, err = sql.Open("sqlite3", openPath)
	if err != nil {
		return err
	}

	// Table creation is indempotent, run it every time
	err = createDb(d.db)
	if err != nil {
		return fmt.Errorf("Error creating database: %s", err)
	}

	// Run PRAGMA statements now since they are *per-connection*.
	d.db.Exec("PRAGMA foreign_keys=ON;") // This allows us to use ON DELETE CASCADE

	v := dbGetSchema(d.db)

	if v != DB_CURRENT_VERSION {
		err = dbUpdate(d, v)
		if err != nil {
			return err
		}
	}

	return nil
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

func isNoMatchError(err error) bool {
	if err == nil {
		return false
	}
	if err.Error() == "sql: no rows in result set" {
		return true
	}
	return false
}

func dbBegin(db *sql.DB) (*sql.Tx, error) {
	for i := 0; i < 100; i++ {
		tx, err := db.Begin()
		if err == nil {
			return tx, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbBegin: error %q", err)
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DbBegin: DB still locked")
	shared.PrintStack()
	return nil, fmt.Errorf("DB is locked")
}

func txCommit(tx *sql.Tx) error {
	for i := 0; i < 100; i++ {
		err := tx.Commit()
		if err == nil {
			return nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("Txcommit: error %q", err)
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("Txcommit: db still locked")
	shared.PrintStack()
	return fmt.Errorf("DB is locked")
}

func dbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	for i := 0; i < 100; i++ {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if isNoMatchError(err) {
			return err
		}
		if !isDbLockedError(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DbQueryRowScan: query %q args %q, DB still locked", q, args)
	shared.PrintStack()
	return fmt.Errorf("DB is locked")
}

func dbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	for i := 0; i < 100; i++ {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbQuery: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DbQuery: query %q args %q, DB still locked", q, args)
	shared.PrintStack()
	return nil, fmt.Errorf("DB is locked")
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
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s", t)
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
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s", t)
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
	for i := 0; i < 100; i++ {
		result, err := doDbQueryScan(db, q, inargs, outfmt)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbQuery: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DbQueryscan: query %q inargs %q, DB still locked", q, inargs)
	shared.PrintStack()
	return nil, fmt.Errorf("DB is locked")
}

func dbExec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
	for i := 0; i < 100; i++ {
		result, err := db.Exec(q, args...)
		if err == nil {
			return result, nil
		}
		if !isDbLockedError(err) {
			shared.Debugf("DbExec: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DbExec: query %q args %q, DB still locked", q, args)
	shared.PrintStack()
	return nil, fmt.Errorf("DB is locked")
}
