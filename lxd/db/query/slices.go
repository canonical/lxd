package query

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// SelectURIs returns a list of LXD API URI strings for the resource yielded by
// the given query.
//
// The f argument must be a function that formats the entity URI using the
// columns yielded by the query.
func SelectURIs(stmt *sql.Stmt, f func(a ...interface{}) string, args ...interface{}) ([]string, error) {
	rows, err := stmt.Query(args...)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to query URIs")
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, errors.Wrap(err, "Rows columns")
	}

	params := make([]interface{}, len(columns))

	dest := make([]interface{}, len(params))
	for i := range params {
		params[i] = ""
		dest[i] = &params[i]
	}

	uris := []string{}

	for rows.Next() {
		err := rows.Scan(dest...)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to scan URI params")
		}

		uri := f(params...)
		uris = append(uris, uri)
	}

	err = rows.Err()
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to close URI result set")
	}

	return uris, nil
}

// SelectStrings executes a statement which must yield rows with a single string
// column. It returns the list of column values.
func SelectStrings(tx *sql.Tx, query string, args ...interface{}) ([]string, error) {
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

	err := scanSingleColumn(tx, query, args, "TEXT", scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// SelectIntegers executes a statement which must yield rows with a single integer
// column. It returns the list of column values.
func SelectIntegers(tx *sql.Tx, query string, args ...interface{}) ([]int, error) {
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

	err := scanSingleColumn(tx, query, args, "INTEGER", scan)
	if err != nil {
		return nil, err
	}

	return values, nil
}

// InsertStrings inserts a new row for each of the given strings, using the
// given insert statement template, which must define exactly one insertion
// column and one substitution placeholder for the values. For example:
// InsertStrings(tx, "INSERT INTO foo(name) VALUES %s", []string{"bar"}).
func InsertStrings(tx *sql.Tx, stmt string, values []string) error {
	n := len(values)

	if n == 0 {
		return nil
	}

	params := make([]string, n)
	args := make([]interface{}, n)
	for i, value := range values {
		params[i] = "(?)"
		args[i] = value
	}

	stmt = fmt.Sprintf(stmt, strings.Join(params, ", "))
	_, err := tx.Exec(stmt, args...)
	return err
}

// Execute the given query and ensure that it yields rows with a single column
// of the given database type. For every row yielded, execute the given
// scanner.
func scanSingleColumn(tx *sql.Tx, query string, args []interface{}, typeName string, scan scanFunc) error {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

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
