package connection

/*
import (
	"github.com/CanonicalLtd/go-sqlite3"
	"github.com/pkg/errors"
)

// OpenLeader is a wrapper around SQLiteDriver.Open that opens connection in
// leader replication mode, and sets any additional dqlite-related options.
//
// The 'methods' argument is used to set the replication methods.
func OpenLeader(dsn string, methods sqlite3.ReplicationMethods) (*sqlite3.SQLiteConn, error) {
	conn, err := open(dsn)
	if err != nil {
		return nil, err
	}

	// Swith to leader replication mode for this connection.
	if err := conn.ReplicationLeader(methods); err != nil {
		return nil, err
	}

	return conn, nil

}

// OpenFollower is a wrapper around SQLiteDriver.Open that opens connection in
// follower replication mode, and sets any additional dqlite-related options.
func OpenFollower(dsn string) (*sqlite3.SQLiteConn, error) {
	conn, err := open(dsn)
	if err != nil {
		return nil, err
	}

	// Switch to leader replication mode for this connection.
	if err := conn.ReplicationFollower(); err != nil {
		return nil, err
	}

	return conn, nil
}

// Open a SQLite connection, setting anything that is common between leader and
// follower connections.
func open(dsn string) (*sqlite3.SQLiteConn, error) {
	// Open a plain connection.
	driver := &sqlite3.SQLiteDriver{}
	conn, err := driver.Open(dsn)
	if err != nil {
		return nil, errors.Wrapf(err, "open error for %s", dsn)
	}

	// Convert driver.Conn interface to concrete sqlite3.SQLiteConn.
	sqliteConn := conn.(*sqlite3.SQLiteConn)

	// Ensure journal mode is set to WAL, as this is a requirement for
	// replication.
	if _, err := sqliteConn.Exec("PRAGMA journal_mode=wal", nil); err != nil {
		return nil, err
	}

	// Ensure WAL autocheckpoint disabled, since checkpoints are triggered
	// by explicitly by dqlite.
	if _, err := sqliteConn.Exec("PRAGMA wal_autocheckpoint=0", nil); err != nil {
		return nil, err
	}

	// Ensure we don't truncate the WAL.
	if _, err := sqliteConn.Exec("PRAGMA journal_size_limit=-1", nil); err != nil {
		return nil, err
	}

	return sqliteConn, nil
}
*/
