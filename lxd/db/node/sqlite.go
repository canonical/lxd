package node

import (
	"database/sql"

	"github.com/mattn/go-sqlite3"
)

func init() {
	sql.Register("sqlite3_with_fk", &sqlite3.SQLiteDriver{ConnectHook: sqliteEnableForeignKeys})
}

// Opens the node-level database with the correct parameters for LXD.
func sqliteOpen(path string) (*sql.DB, error) {
	// TODO - make the timeout command-line configurable?

	// These are used to tune the transaction BEGIN behavior instead of using the
	// similar "locking_mode" pragma (locking for the whole database connection).
	openPath := path + "?_busy_timeout=5000&_txlock=exclusive"

	// Open the database. If the file doesn't exist it is created.
	return sql.Open("sqlite3_with_fk", openPath)
}

func sqliteEnableForeignKeys(conn *sqlite3.SQLiteConn) error {
	_, err := conn.Exec("PRAGMA foreign_keys=ON;", nil)
	return err
}
