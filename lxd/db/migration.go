package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/pkg/errors"
)

// LoadPreClusteringData loads all the data that before the introduction of
// LXD clustering used to live in the node-level database.
//
// This is used for performing a one-off data migration when a LXD instance is
// upgraded from a version without clustering to a version that supports
// clustering, since in those version all data lives in the cluster database
// (regardless of whether clustering is actually on or off).
func LoadPreClusteringData(tx *sql.Tx) (*Dump, error) {
	// Dump all tables.
	dump := &Dump{
		Schema: map[string][]string{},
		Data:   map[string][][]interface{}{},
	}
	for _, table := range preClusteringTables {
		data := [][]interface{}{}
		stmt := fmt.Sprintf("SELECT * FROM %s", table)
		rows, err := tx.Query(stmt)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to fetch rows from %s", table)
		}
		columns, err := rows.Columns()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get columns of %s", table)
		}
		dump.Schema[table] = columns

		for rows.Next() {
			values := make([]interface{}, len(columns))
			row := make([]interface{}, len(columns))
			for i := range values {
				row[i] = &values[i]
			}
			err := rows.Scan(row...)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to scan row from %s", table)
			}
			data = append(data, values)
		}
		err = rows.Err()
		if err != nil {
			return nil, errors.Wrapf(err, "error while fetching rows from %s", table)
		}

		dump.Data[table] = data
	}

	return dump, nil
}

// ImportPreClusteringData imports the data loaded with LoadPreClusteringData.
func (c *Cluster) ImportPreClusteringData(dump *Dump) error {
	tx, err := c.db.Begin()
	if err != nil {
		return errors.Wrap(err, "failed to start cluster database transaction")
	}

	for _, table := range preClusteringTables {
		for i, row := range dump.Data[table] {
			for i, element := range row {
				// Convert []byte columns to string. This is safe to do since
				// the pre-clustering schema only had TEXT fields and no BLOB.
				bytes, ok := element.([]byte)
				if ok {
					row[i] = string(bytes)
				}
			}
			columns := dump.Schema[table]
			switch table {
			case "networks_config":
				columns = append(columns, "node_id")
				row = append(row, int64(1))
			}
			stmt := fmt.Sprintf("INSERT INTO %s(%s)", table, strings.Join(columns, ", "))
			stmt += fmt.Sprintf(" VALUES %s", query.Params(len(columns)))
			result, err := tx.Exec(stmt, row...)
			if err != nil {
				return errors.Wrapf(err, "failed to insert row %d into %s", i, table)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return errors.Wrapf(err, "no result count for row %d of %s", i, table)
			}
			if n != 1 {
				return fmt.Errorf("could not insert %d int %s", i, table)
			}
		}
	}

	return tx.Commit()
}

// Dump is a dump of all the user data in lxd.db prior the migration to the
// cluster db.
type Dump struct {
	// Map table names to the names or their columns.
	Schema map[string][]string

	// Map a table name to all the rows it contains. Each row is a slice
	// of interfaces.
	Data map[string][][]interface{}
}

var preClusteringTables = []string{
	"config",
	"networks",
	"networks_config",
}
