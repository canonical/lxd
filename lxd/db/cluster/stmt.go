package cluster

import (
	"database/sql"
	"fmt"

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
func PrepareStmts(db *sql.DB, skipErrors bool) (map[int]*sql.Stmt, error) {
	index := map[int]*sql.Stmt{}

	for code, sql := range stmts {
		stmt, err := db.Prepare(sql)
		if err != nil && !skipErrors {
			return nil, errors.Wrapf(err, "%q", sql)
		}
		index[code] = stmt
	}

	return index, nil
}

// GetRegisteredStmt returns the SQL statement string from its registration code.
func GetRegisteredStmt(code int) string {
	sql, ok := stmts[code]
	if !ok {
		panic(fmt.Sprintf("No statement registered with code :%d", code))
	}
	return sql
}

// RegisterStmtIfNew registers a new SQL statement if it has not already been registered.
func RegisterStmtIfNew(sql string) int {
	for k, v := range stmts {
		if v == sql {
			return k
		}
	}

	return RegisterStmt(sql)
}

var stmts = map[int]string{} // Statement code to statement SQL text
