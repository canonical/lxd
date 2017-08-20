// +build go1.8

package query

import (
	"database/sql"
	"fmt"
	"strings"
)

// Check that the given result set yields rows with a single column of a
// specific type.
func checkRowsHaveOneColumnOfSpecificType(rows *sql.Rows, typeName string) error {
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if len(types) != 1 {
		return fmt.Errorf("query yields %d columns, not 1", len(types))
	}
	actualTypeName := strings.ToUpper(types[0].DatabaseTypeName())
	if actualTypeName != typeName {
		return fmt.Errorf("query yields %s column, not %s", actualTypeName, typeName)
	}
	return nil
}
