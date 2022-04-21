package query

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dump returns a SQL text dump of all rows across all tables, similar to
// sqlite3's dump feature.
func Dump(ctx context.Context, tx *sql.Tx, schemaOnly bool) (string, error) {
	tablesSchemas, tableNames, err := getTablesSchemas(ctx, tx)
	if err != nil {
		return "", err
	}

	// Begin dump string.
	var builder strings.Builder
	builder.WriteString("PRAGMA foreign_keys=OFF;\n")
	builder.WriteString("BEGIN TRANSACTION;\n")

	// For each table, write the schema and optionally write the data.
	for _, tableName := range tableNames {
		builder.WriteString(tablesSchemas[tableName] + "\n")

		if !schemaOnly {
			tableData, err := getTableData(ctx, tx, tableName)
			if err != nil {
				return "", err
			}

			for _, stmt := range tableData {
				builder.WriteString(stmt + "\n")
			}
		}
	}

	// Sequences (unless the schemaOnly flag is true).
	if !schemaOnly {
		builder.WriteString("DELETE FROM sqlite_sequence;\n")

		tableData, err := getTableData(ctx, tx, "sqlite_sequence")
		if err != nil {
			return "", fmt.Errorf("Failed to dump table sqlite_sequence: %w", err)
		}

		for _, stmt := range tableData {
			builder.WriteString(stmt + "\n")
		}
	}

	// Commit.
	builder.WriteString("COMMIT;\n")

	return builder.String(), nil
}

// getTablesSchemas gets all the tables and their schema, as well as a list of table names in their default order from
// the sqlite_master table.
func getTablesSchemas(ctx context.Context, tx *sql.Tx) (map[string]string, []string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY rowid`)
	if err != nil {
		return nil, nil, fmt.Errorf("Could not get table names and their schema: %w", err)
	}

	defer rows.Close()

	tablesSchemas := make(map[string]string)
	var names []string
	for rows.Next() {
		var name string
		var schema string
		err := rows.Scan(&name, &schema)
		if err != nil {
			return nil, nil, fmt.Errorf("Could not scan table name and schema: %w", err)
		}

		names = append(names, name)

		// Whether a table name is quoted or not can depend on if it was quoted when originally created, or if it
		// collides with a keyword (and maybe more). Regardless, sqlite3 quotes table names in create statements when
		// executing a dump. If the table name is already quoted, add the "IF NOT EXISTS" clause, else quote it and add
		// the same clause.
		isQuoted := strings.Contains(schema, fmt.Sprintf("TABLE %q", name))
		if isQuoted {
			schema = strings.Replace(schema, "TABLE", "TABLE IF NOT EXISTS", 1)
		} else {
			schema = strings.Replace(schema, name, fmt.Sprintf("IF NOT EXISTS %q", name), 1)
		}

		tablesSchemas[name] = schema + ";"
	}

	return tablesSchemas, names, nil
}

// getTableData gets all the data for a single table, returning a string slice where each element is an insert statement
// for the data.
func getTableData(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	var statements []string

	// Query all rows.
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s ORDER BY rowid", table))
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch rows for table %q: %w", table, err)
	}

	defer rows.Close()

	// Get the column names.
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("Failed to get columns for table %q: %w", table, err)
	}

	// Generate an INSERT statement for each row.
	for i := 0; rows.Next(); i++ {
		raw := make([]any, len(columns)) // Raw column values
		row := make([]any, len(columns))
		for i := range raw {
			row[i] = &raw[i]
		}

		err := rows.Scan(row...)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan row %d in table %q: %w", i, table, err)
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
					return nil, fmt.Errorf("Bad type in column %q of row %d in table %q", columns[j], i, table)
				}

				values[j] = "NULL"
			}
		}

		statement := fmt.Sprintf("INSERT INTO %s VALUES(%s);", table, strings.Join(values, ","))
		statements = append(statements, statement)
	}

	return statements, nil
}
