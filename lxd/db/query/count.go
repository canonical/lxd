package query

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"
)

// Count returns the number of rows in the given table.
func Count(tx *sql.Tx, table string, where string, args ...interface{}) (int, error) {
	stmt := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if where != "" {
		stmt += fmt.Sprintf(" WHERE %s", where)
	}
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return -1, err
	}
	defer rows.Close()

	// For sanity, make sure we read one and only one row.
	if !rows.Next() {
		return -1, fmt.Errorf("no rows returned")
	}
	var count int
	err = rows.Scan(&count)
	if err != nil {
		return -1, fmt.Errorf("failed to scan count column")
	}
	if rows.Next() {
		return -1, fmt.Errorf("more than one row returned")
	}
	err = rows.Err()
	if err != nil {
		return -1, err
	}

	return count, nil
}

// CountAll returns a map associating each table name in the database
// with the total count of its rows.
func CountAll(tx *sql.Tx) (map[string]int, error) {
	tables, err := SelectStrings(tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch table names")
	}

	counts := map[string]int{}
	for _, table := range tables {
		count, err := Count(tx, table, "")
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to count rows of %s", table)
		}
		counts[table] = count
	}

	return counts, nil
}
