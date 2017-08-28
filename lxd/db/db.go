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
	return len(updates)
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

func doDbQueryScan(qi queryer, q string, args []interface{}, outargs []interface{}) ([][]interface{}, error) {
	rows, err := qi.Query(q, args...)
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

type queryer interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

/*
 * . qi anything implementing the querier interface (i.e. either sql.DB or sql.Tx)
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
func QueryScan(qi queryer, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
	for i := 0; i < 1000; i++ {
		result, err := doDbQueryScan(qi, q, inargs, outfmt)
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
