package main

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

func dbConfigValuesGet(db *sql.DB) (map[string]string, error) {
	q := "SELECT key, value FROM config"
	rows, err := dbQuery(db, q)
	if err != nil {
		return map[string]string{}, err
	}
	defer rows.Close()

	results := map[string]string{}

	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		results[key] = value
	}

	return results, nil
}

func dbConfigValueSet(db *sql.DB, key string, value string) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM config WHERE key=?", key)
	if err != nil {
		tx.Rollback()
		return err
	}

	if value != "" {
		str := `INSERT INTO config (key, value) VALUES (?, ?);`
		stmt, err := tx.Prepare(str)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()
		_, err = stmt.Exec(key, value)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = txCommit(tx)
	if err != nil {
		return err
	}

	return nil
}
