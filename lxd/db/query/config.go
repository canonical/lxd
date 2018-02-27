package query

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// SelectConfig executes a query statement against a "config" table, which must
// have 'key' and 'value' columns. By default this query returns all keys, but
// additional WHERE filters can be specified.
//
// Returns a map of key names to their associated values.
func SelectConfig(tx *sql.Tx, table string, where string, args ...interface{}) (map[string]string, error) {
	query := fmt.Sprintf("SELECT key, value FROM %s", table)
	if where != "" {
		query += fmt.Sprintf(" WHERE %s", where)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var key string
		var value string

		err := rows.Scan(&key, &value)
		if err != nil {
			return nil, err
		}

		values[key] = value
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return values, nil
}

// UpdateConfig updates the given keys in the given table. Config keys set to
// empty values will be deleted.
func UpdateConfig(tx *sql.Tx, table string, values map[string]string) error {
	changes := map[string]string{}
	deletes := []string{}

	for key, value := range values {
		if value == "" {
			deletes = append(deletes, key)
			continue
		}
		changes[key] = value
	}

	err := upsertConfig(tx, table, changes)
	if err != nil {
		return errors.Wrap(err, "updating values failed")
	}

	err = deleteConfig(tx, table, deletes)
	if err != nil {
		return errors.Wrap(err, "deleting values failed")
	}

	return nil
}

// Insert or updates the key/value rows of the given config table.
func upsertConfig(tx *sql.Tx, table string, values map[string]string) error {
	if len(values) == 0 {
		return nil // Nothing to update
	}

	query := fmt.Sprintf("INSERT OR REPLACE INTO %s (key, value) VALUES", table)
	exprs := []string{}
	params := []interface{}{}
	for key, value := range values {
		exprs = append(exprs, "(?, ?)")
		params = append(params, key)
		params = append(params, value)
	}
	query += strings.Join(exprs, ",")
	_, err := tx.Exec(query, params...)
	return err
}

// Delete the given key rows from the given config table.
func deleteConfig(tx *sql.Tx, table string, keys []string) error {
	n := len(keys)

	if n == 0 {
		return nil // Nothing to delete.
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE key IN %s", table, Params(n))
	values := make([]interface{}, n)
	for i, key := range keys {
		values[i] = key
	}
	_, err := tx.Exec(query, values...)
	return err
}
