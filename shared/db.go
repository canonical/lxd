package shared

import (
	"database/sql"
	"time"

	"github.com/mattn/go-sqlite3"
)

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
	slept := time.Millisecond * 0
	for {
		err := tx.Commit()
		if err == nil {
			return nil
		}
		if !IsDbLockedError(err) {
			Debugf("Txcommit: error %q\n", err)
			return err
		}
		if slept == 30*time.Second {
			Debugf("DB Locked for 30 seconds\n")
			return err
		}
		time.Sleep(100 * time.Millisecond)
		slept = slept + 100*time.Millisecond
	}
}

func DbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	slept := time.Millisecond * 0
	for {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbQuery: queryrow %q error %q\n", q, err)
			return err
		}
		if slept == 30*time.Second {
			Debugf("DB Locked for 30 seconds\n")
			return err
		}
		time.Sleep(100 * time.Millisecond)
		slept = slept + 100*time.Millisecond
	}
}

func DbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	slept := time.Millisecond * 0
	for {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbQuery: query %q error %q\n", q, err)
			return nil, err
		}
		if slept == 30*time.Second {
			Debugf("DB Locked for 30 seconds\n")
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
		slept = slept + 100*time.Millisecond
	}
}

func DbExec(db *sql.DB, q string, args ...interface{}) (sql.Result, error) {
	slept := time.Millisecond * 0
	for {
		result, err := db.Exec(q, args...)
		if err == nil {
			return result, nil
		}
		if !IsDbLockedError(err) {
			Debugf("DbExec: query %q error %q\n", q, err)
			return nil, err
		}
		if slept == 30*time.Second {
			Debugf("DB Locked for 30 seconds\n")
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
		slept = slept + 100*time.Millisecond
	}
}
