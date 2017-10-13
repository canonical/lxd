package query

import (
	"database/sql"
	"fmt"
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
