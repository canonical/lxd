// +build !go1.8

package query

import "database/sql"

// Check that the given result set yields rows with a single column of a
// specific type.
func checkRowsHaveOneColumnOfSpecificType(rows *sql.Rows, typeName string) error {
	// The Rows.ColumnTypes() method is available only since Go 1.8, so we
	// just return nil for <1.8. This is safe to do since if the returned
	// rows are not of the expected type, call sites will still fail at
	// Rows.Scan() time, although the error message will be less clear.
	return nil
}
