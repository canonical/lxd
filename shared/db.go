package shared

/*
 * This file contains helpers for interacting with the database
 * which will check for database errors at the various steps,
 * as well as re-try indefinately if the db is locked.
 */

import (
	"database/sql"
	"fmt"
	"runtime"
	"time"

	"github.com/mattn/go-sqlite3"
)

func PrintStack() {
	if !debug || logger == nil {
		return
	}

	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	Debugf("%s", buf)
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

func TxCommit(tx *sql.Tx) error {
	for {
		err := tx.Commit()
		if err == nil {
			return nil
		}
		if !IsDbLockedError(err) {
			Debugf("Txcommit: error %q\n", err)
			return err
		}
		Debugf("Txcommit: db was locked\n")
		PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func DbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	for {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbQuery: query %q error %q\n", q, err)
			return err
		}
		Debugf("DbQueryRowScan: query %q args %q, DB was locked\n", q, args)
		PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func DbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	for {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbQuery: query %q error %q\n", q, err)
			return nil, err
		}
		Debugf("DbQuery: query %q args %q, DB was locked\n", q, args)
		PrintStack()
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
		for i, _ := range outargs {
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
		for i, _ := range ptrargs {
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
func DbQueryScan(db *sql.DB, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
	for {
		result, err := doDbQueryScan(db, q, inargs, outfmt)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbQuery: query %q error %q\n", q, err)
			return nil, err
		}
		Debugf("DbQueryscan: query %q inargs %q, DB was locked\n", q, inargs)
		PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func DbExec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
	for {
		result, err := db.Exec(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbExec: query %q error %q\n", q, err)
			return nil, err
		}
		Debugf("DbExec: query %q args %q, DB was locked\n", q, args)
		PrintStack()
		time.Sleep(1 * time.Second)
	}
}
