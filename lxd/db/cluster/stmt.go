package cluster

import (
	"database/sql"

	"github.com/pkg/errors"
)

// RegisterStmt register a SQL statement.
//
// Registered statements will be prepared upfront and re-used, to speed up
// execution.
//
// Return a unique registration code.
func RegisterStmt(sql string) int {
	code := len(stmts)
	stmts[code] = sql
	return code
}

// PrepareStmts prepares all registered statements and returns an index from
// statement code to prepared statement object.
func PrepareStmts(db *sql.DB) (map[int]*sql.Stmt, error) {
	index := map[int]*sql.Stmt{}

	for code, sql := range stmts {
		stmt, err := db.Prepare(sql)
		if err != nil {
			return nil, errors.Wrapf(err, "%q", sql)
		}
		index[code] = stmt
	}

	return index, nil
}

var stmts = map[int]string{} // Statement code to statement SQL text
