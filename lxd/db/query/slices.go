package query

import (
	"context"
	"database/sql"
)

// SelectStrings executes a statement which must yield rows with a single string
// column. It returns the list of column values.
func SelectStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
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

	err := scanSingleColumn(ctx, tx, query, args, scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// SelectIntegers executes a statement which must yield rows with a single integer
// column. It returns the list of column values.
func SelectIntegers(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int, error) {
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

	err := scanSingleColumn(ctx, tx, query, args, scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// Execute the given query and ensure that it yields rows with a single column.
// For every row yielded, execute the given scanner.
func scanSingleColumn(ctx context.Context, tx *sql.Tx, query string, args []any, scan scanFunc) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

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
