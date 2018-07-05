package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db/node"
	"github.com/lxc/lxd/lxd/db/query"
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

// Node mediates access to LXD's data stored in the node-local SQLite database.
type Node struct {
	db *sql.DB // Handle to the node-local SQLite database file.

}

// OpenNode creates a new Node object.
//
// The fresh hook parameter is used by the daemon to mark all known patch names
// as applied when a brand new database is created.
//
// The legacyPatches parameter is used as a mean to apply the legacy V10, V11,
// V15, V29 and V30 non-db updates during the database upgrade sequence, to
// avoid any change in semantics wrt the old logic (see PR #3322).
func OpenNode(dir string, fresh func(*Node) error, legacyPatches map[int]*LegacyPatch) (*Node, error) {
	db, err := node.Open(dir)
	if err != nil {
		return nil, err
	}

	hook := legacyPatchHook(db, legacyPatches)
	initial, err := node.EnsureSchema(db, dir, hook)
	if err != nil {
		return nil, err
	}

	node := &Node{
		db: db,
	}

	if initial == 0 {
		err := node.ProfileCreateDefault()
		if err != nil {
			return nil, err
		}
		if fresh != nil {
			err := fresh(node)
			if err != nil {
				return nil, err
			}
		}
	}

	err = node.ProfileCreateDocker()
	if err != nil {
		return nil, err
	}

	return node, nil
}

// ForLegacyPatches is a aid for the hack in initializeDbObject, which sets
// the db-related Deamon attributes upfront, to be backward compatible with the
// legacy patches that need to interact with the database.
func ForLegacyPatches(db *sql.DB) *Node {
	return &Node{db: db}
}

// DB returns the low level database handle to the node-local SQLite
// database.
//
// FIXME: this is used for compatibility with some legacy code, and should be
//        dropped once there are no call sites left.
func (n *Node) DB() *sql.DB {
	return n.db
}

// Transaction creates a new NodeTx object and transactionally executes the
// node-level database interactions invoked by the given function. If the
// function returns no error, all database changes are committed to the
// node-level database, otherwise they are rolled back.
func (n *Node) Transaction(f func(*NodeTx) error) error {
	nodeTx := &NodeTx{}
	return query.Transaction(n.db, func(tx *sql.Tx) error {
		nodeTx.tx = tx
		return f(nodeTx)
	})
}

// Close the database facade.
func (n *Node) Close() error {
	return n.db.Close()
}

// Begin a new transaction against the local database. Legacy method.
func (n *Node) Begin() (*sql.Tx, error) {
	return begin(n.db)
}

// UpdateSchemasDotGo updates the schema.go files in the local/ and cluster/
// sub-packages.
func UpdateSchemasDotGo() error {
	err := node.SchemaDotGo()
	if err != nil {
		return fmt.Errorf("failed to update local schema.go: %v", err)
	}

	return nil
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

func begin(db *sql.DB) (*sql.Tx, error) {
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

	logger.Debugf("DbQuery: query %q args %q, DB still locked", q, []interface{}(args))
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
func queryScan(qi queryer, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
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

func exec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
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

	logger.Debugf("DbExec: query %q args %q, DB still locked", q, []interface{}(args))
	logger.Debugf(logger.GetStack())
	return nil, fmt.Errorf("DB is locked")
}
