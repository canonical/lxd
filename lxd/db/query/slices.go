package query

import (
	"database/sql"
)

// SelectStrings executes a statement which must yield rows with a single string
// column. It returns the list of column values.
func SelectStrings(tx *sql.Tx, query string) ([]string, error) {
	values := []string{}
	scan := func(rows *sql.Rows) error {
		var value string
		err := rows.Scan(&value)
		if err != nil {
			return err
		}
		values = append(values, value)
		return nil
	}

	err := scanSingleColumn(tx, query, "TEXT", scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// SelectIntegers executes a statement which must yield rows with a single integer
// column. It returns the list of column values.
func SelectIntegers(tx *sql.Tx, query string) ([]int, error) {
	values := []int{}
	scan := func(rows *sql.Rows) error {
		var value int
		err := rows.Scan(&value)
		if err != nil {
			return err
		}
		values = append(values, value)
		return nil
	}

	err := scanSingleColumn(tx, query, "INTEGER", scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// Execute the given query and ensure that it yields rows with a single column
// of the given database type. For every row yielded, execute the given
// scanner.
func scanSingleColumn(tx *sql.Tx, query string, typeName string, scan scanFunc) error {
	rows, err := tx.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	err = checkRowsHaveOneColumnOfSpecificType(rows, typeName)
	if err != nil {
		return err
	}

	for rows.Next() {
		err := scan(rows)
		if err != nil {
			return err
		}
	}

	err = rows.Err()
	if err != nil {
		return err
	}
	return nil
}

// Function to scan a single row.
type scanFunc func(*sql.Rows) error
