package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
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
		logger.Debugf("Loading data from table %s", table)
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

	// Delete the default profile in the cluster database, which always
	// gets created no matter what.
	_, err = tx.Exec("DELETE FROM profiles WHERE id=1")
	if err != nil {
		return errors.Wrap(err, "failed to delete default profile")
	}

	for _, table := range preClusteringTables {
		logger.Debugf("Migrating data for table %s", table)
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

			nullNodeID := false // Whether node-related rows should have a NULL node ID
			appendNodeID := func() {
				columns = append(columns, "node_id")
				if nullNodeID {
					row = append(row, nil)
				} else {
					row = append(row, int64(1))
				}
			}

			switch table {
			case "config":
				// Don't migrate the core.https_address config key,
				// which is node-specific and must remain in the node
				// database.
				isCoreHTTPSAddress := false
				for i, column := range columns {
					if column == "key" && row[i] == "core.https_address" {
						isCoreHTTPSAddress = true
					}
				}
				if isCoreHTTPSAddress {
					continue
				}
			case "containers":
				appendNodeID()
			case "networks_config":
				// The keys listed in NetworkNodeConfigKeys
				// are the only ones which are not global to the
				// cluster, so all other keys will have a NULL
				// node_id.
				index := 0
				for i, column := range columns {
					if column == "key" {
						index = i
						break
					}
				}
				key := row[index].(string)
				if !shared.StringInSlice(key, NetworkNodeConfigKeys) {
					nullNodeID = true
					break
				}
				appendNodeID()
			case "storage_pools_config":
				// The keys listed in StoragePoolNodeConfigKeys
				// are the only ones which are not global to the
				// cluster, so all other keys will have a NULL
				// node_id.
				index := 0
				for i, column := range columns {
					if column == "key" {
						index = i
						break
					}
				}
				key := row[index].(string)
				if !shared.StringInSlice(key, StoragePoolNodeConfigKeys) {
					nullNodeID = true
					break
				}
				appendNodeID()
			case "networks":
				fallthrough
			case "storage_pools":
				columns = append(columns, "state")
				row = append(row, storagePoolCreated)
			case "storage_volumes":
				appendNodeID()
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

			// Also insert the image ID -> node ID association.
			if shared.StringInSlice(table, []string{"images", "networks", "storage_pools"}) {
				entity := table[:len(table)-1]
				importNodeAssociation(entity, columns, row, tx)
			}
		}
	}

	return tx.Commit()
}

// Insert a row in one of the nodes association tables (storage_pools_nodes,
// networks_nodes, images_nodes).
func importNodeAssociation(entity string, columns []string, row []interface{}, tx *sql.Tx) error {
	stmt := fmt.Sprintf(
		"INSERT INTO %ss_nodes(%s_id, node_id) VALUES(?, 1)", entity, entity)
	var id int64
	for i, column := range columns {
		if column == "id" {
			id = row[i].(int64)
			break
		}
	}
	if id == 0 {
		return fmt.Errorf("entity %s has invalid ID", entity)
	}
	_, err := tx.Exec(stmt, row...)
	if err != nil {
		return errors.Wrapf(err, "failed to associate %s to node", entity)
	}
	return nil
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
	"certificates",
	"config",
	"profiles",
	"profiles_config",
	"profiles_devices",
	"profiles_devices_config",
	"containers",
	"containers_config",
	"containers_devices",
	"containers_devices_config",
	"containers_profiles",
	"images",
	"images_aliases",
	"images_properties",
	"images_source",
	"networks",
	"networks_config",
	"storage_pools",
	"storage_pools_config",
	"storage_volumes",
	"storage_volumes_config",
}
