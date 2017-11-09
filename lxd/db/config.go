package db

import (
	"database/sql"

	"github.com/lxc/lxd/lxd/db/query"
)

// Config fetches all LXD node-level config keys.
func (n *NodeTx) Config() (map[string]string, error) {
	return query.SelectConfig(n.tx, "config")
}

// UpdateConfig updates the given LXD node-level configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (n *NodeTx) UpdateConfig(values map[string]string) error {
	return query.UpdateConfig(n.tx, "config", values)
}

func ConfigValuesGet(db *sql.DB) (map[string]string, error) {
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

func ConfigValueSet(db *sql.DB, key string, value string) error {
	tx, err := begin(db)
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

	err = TxCommit(tx)
	if err != nil {
		return err
	}

	return nil
}
