package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared/logger"
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
    description TEXT,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME,
    last_use_date DATETIME,
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
    auto_update INTEGER NOT NULL DEFAULT 0,
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
    description TEXT,
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
CREATE TABLE IF NOT EXISTS images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias VARCHAR(255) NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (network_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
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
);
CREATE TABLE IF NOT EXISTS storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
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
    description TEXT,
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

func enableForeignKeys(conn *sqlite3.SQLiteConn) error {
	_, err := conn.Exec("PRAGMA foreign_keys=ON;", nil)
	return err
}

func init() {
	sql.Register("sqlite3_with_fk", &sqlite3.SQLiteDriver{ConnectHook: enableForeignKeys})
}

// OpenDb opens the database with the correct parameters for LXD.
func OpenDb(path string) (*sql.DB, error) {
	timeout := 5 // TODO - make this command-line configurable?

	// These are used to tune the transaction BEGIN behavior instead of using the
	// similar "locking_mode" pragma (locking for the whole database connection).
	openPath := fmt.Sprintf("%s?_busy_timeout=%d&_txlock=exclusive", path, timeout*1000)

	// Open the database. If the file doesn't exist it is created.
	return sql.Open("sqlite3_with_fk", openPath)
}

// Create the initial (current) schema for a given SQLite DB connection.
func CreateDb(db *sql.DB, patchNames []string) (err error) {
	latestVersion := GetSchema(db)

	if latestVersion != 0 {
		return nil
	}

	_, err = db.Exec(CURRENT_SCHEMA)
	if err != nil {
		return err
	}

	// There isn't an entry for schema version, let's put it in.
	insertStmt := `INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err = db.Exec(insertStmt, GetLatestSchema())
	if err != nil {
		return err
	}

	// Mark all existing patches as applied
	for _, patchName := range patchNames {
		PatchesMarkApplied(db, patchName)
	}

	err = ProfileCreateDefault(db)
	if err != nil {
		return err
	}

	return nil
}

func GetSchema(db *sql.DB) (v int) {
	arg1 := []interface{}{}
	arg2 := []interface{}{&v}
	q := "SELECT max(version) FROM schema"
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return 0
	}
	return v
}

func GetLatestSchema() int {
	if len(dbUpdates) == 0 {
		return 0
	}

	return dbUpdates[len(dbUpdates)-1].version
}

func IsDbLockedError(err error) bool {
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

func Begin(db *sql.DB) (*sql.Tx, error) {
	for i := 0; i < 1000; i++ {
		tx, err := db.Begin()
		if err == nil {
			return tx, nil
		}
		if !IsDbLockedError(err) {
			logger.Debugf("DbBegin: error %q", err)
			return nil, err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbBegin: DB still locked")
	logger.Debugf(logger.GetStack())
	return nil, fmt.Errorf("DB is locked")
}

func TxCommit(tx *sql.Tx) error {
	for i := 0; i < 1000; i++ {
		err := tx.Commit()
		if err == nil {
			return nil
		}
		if !IsDbLockedError(err) {
			logger.Debugf("Txcommit: error %q", err)
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("Txcommit: db still locked")
	logger.Debugf(logger.GetStack())
	return fmt.Errorf("DB is locked")
}

func dbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	for i := 0; i < 1000; i++ {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if isNoMatchError(err) {
			return err
		}
		if !IsDbLockedError(err) {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbQueryRowScan: query %q args %q, DB still locked", q, args)
	logger.Debugf(logger.GetStack())
	return fmt.Errorf("DB is locked")
}

func dbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	for i := 0; i < 1000; i++ {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			logger.Debugf("DbQuery: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbQuery: query %q args %q, DB still locked", q, args)
	logger.Debugf(logger.GetStack())
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
			case int64:
				integer := int64(0)
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
			case int64:
				newargs[i] = *ptrargs[i].(*int64)
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
func QueryScan(db *sql.DB, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
	for i := 0; i < 1000; i++ {
		result, err := doDbQueryScan(db, q, inargs, outfmt)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			logger.Debugf("DbQuery: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbQueryscan: query %q inargs %q, DB still locked", q, inargs)
	logger.Debugf(logger.GetStack())
	return nil, fmt.Errorf("DB is locked")
}

func Exec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
	for i := 0; i < 1000; i++ {
		result, err := db.Exec(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			logger.Debugf("DbExec: query %q error %q", q, err)
			return nil, err
		}
		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbExec: query %q args %q, DB still locked", q, args)
	logger.Debugf(logger.GetStack())
	return nil, fmt.Errorf("DB is locked")
}
