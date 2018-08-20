package query

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// Dump returns a SQL text dump of all rows across all tables, similar to
// sqlite3's dump feature
func Dump(tx *sql.Tx, schema string, schemaOnly bool) (string, error) {
	schemas := dumpParseSchema(schema)

	// Begin
	dump := `PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
`
	// Schema table
	tableDump, err := dumpTable(tx, "schema", dumpSchemaTable)
	if err != nil {
		return "", errors.Wrapf(err, "failed to dump table schema")
	}
	dump += tableDump

	// All other tables
	tables := make([]string, 0)
	for table := range schemas {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if schemaOnly {
			// Dump only the schema.
			dump += schemas[table] + "\n"
			continue
		}
		tableDump, err := dumpTable(tx, table, schemas[table])
		if err != nil {
			return "", errors.Wrapf(err, "failed to dump table %s", table)
		}
		dump += tableDump
	}

	// Sequences (unless the schemaOnly flag is true)
	if !schemaOnly {
		tableDump, err = dumpTable(tx, "sqlite_sequence", "DELETE FROM sqlite_sequence;")
		if err != nil {
			return "", errors.Wrapf(err, "failed to dump table sqlite_sequence")
		}
		dump += tableDump
	}

	// Commit
	dump += "COMMIT;\n"

	return dump, nil
}

// Return a map from table names to their schema definition, taking a full
// schema SQL text generated with schema.Schema.Dump().
func dumpParseSchema(schema string) map[string]string {
	tables := map[string]string{}
	for _, statement := range strings.Split(schema, ";") {
		statement = strings.Trim(statement, " \n") + ";"
		if !strings.HasPrefix(statement, "CREATE TABLE") {
			continue
		}
		table := strings.Split(statement, " ")[2]
		tables[table] = statement
	}
	return tables
}

// Dump a single table, returning a SQL text containing statements for its
// schema and data.
func dumpTable(tx *sql.Tx, table, schema string) (string, error) {
	statements := []string{schema}

	// Query all rows.
	rows, err := tx.Query(fmt.Sprintf("SELECT * FROM %s ORDER BY rowid", table))
	if err != nil {
		return "", errors.Wrap(err, "failed to fetch rows")
	}
	defer rows.Close()

	// Figure column names
	columns, err := rows.Columns()
	if err != nil {
		return "", errors.Wrap(err, "failed to get columns")
	}

	// Generate an INSERT statement for each row.
	for i := 0; rows.Next(); i++ {
		raw := make([]interface{}, len(columns)) // Raw column values
		row := make([]interface{}, len(columns))
		for i := range raw {
			row[i] = &raw[i]
		}
		err := rows.Scan(row...)
		if err != nil {
			return "", errors.Wrapf(err, "failed to scan row %d", i)
		}
		values := make([]string, len(columns))
		for j, v := range raw {
			switch v := v.(type) {
			case int64:
				values[j] = strconv.FormatInt(v, 10)
			case string:
				values[j] = fmt.Sprintf("'%s'", v)
			case []byte:
				values[j] = fmt.Sprintf("'%s'", string(v))
			case time.Time:
				values[j] = strconv.FormatInt(v.Unix(), 10)
			default:
				if v != nil {
					return "", fmt.Errorf("bad type in column %s of row %d", columns[j], i)
				}
				values[j] = "NULL"
			}
		}
		statement := fmt.Sprintf("INSERT INTO %s VALUES(%s);", table, strings.Join(values, ","))
		statements = append(statements, statement)
	}
	return strings.Join(statements, "\n") + "\n", nil
}

// Schema of the schema table.
const dumpSchemaTable = `CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);`
