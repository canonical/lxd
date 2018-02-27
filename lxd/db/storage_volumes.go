package db

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/lxc/lxd/lxd/db/query"
)

// StorageVolumeNodeAddresses returns the addresses of all nodes on which the
// volume with the given name if defined.
//
// The empty string is used in place of the address of the current node.
func (c *ClusterTx) StorageVolumeNodeAddresses(poolID int64, name string, typ int) ([]string, error) {
	nodes := []struct {
		id      int64
		address string
	}{}
	dest := func(i int) []interface{} {
		nodes = append(nodes, struct {
			id      int64
			address string
		}{})
		return []interface{}{&nodes[i].id, &nodes[i].address}

	}
	stmt := `
SELECT nodes.id, nodes.address
  FROM nodes JOIN storage_volumes ON storage_volumes.node_id=nodes.id
    WHERE storage_volumes.storage_pool_id=? AND storage_volumes.name=? AND storage_volumes.type=?
`
	err := query.SelectObjects(c.tx, dest, stmt, poolID, name, typ)
	if err != nil {
		return nil, err
	}

	addresses := []string{}
	for _, node := range nodes {
		address := node.address
		if node.id == c.nodeID {
			address = ""
		}
		addresses = append(addresses, address)
	}

	sort.Strings(addresses)

	if len(addresses) == 0 {
		return nil, NoSuchObjectError
	}

	return addresses, nil
}

// StorageVolumeNodeGet returns the name of the node a storage volume is on.
func (c *Cluster) StorageVolumeNodeGet(volumeID int64) (string, error) {
	name := ""
	query := `
SELECT nodes.name FROM storage_volumes
  JOIN nodes ON nodes.id=storage_volumes.node_id
   WHERE storage_volumes.id=?
`
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&name}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", NoSuchObjectError
		}
	}

	return name, nil
}

// Get config of a storage volume.
func (c *Cluster) StorageVolumeConfigGet(volumeID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_volumes_config WHERE storage_volume_id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{key, value}

	results, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return nil, err
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

// Get the description of a storage volume.
func (c *Cluster) StorageVolumeDescriptionGet(volumeID int64) (string, error) {
	description := sql.NullString{}
	query := "SELECT description FROM storage_volumes WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&description}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", NoSuchObjectError
		}
	}

	return description.String, nil
}

// Update description of a storage volume.
func StorageVolumeDescriptionUpdate(tx *sql.Tx, volumeID int64, description string) error {
	_, err := tx.Exec("UPDATE storage_volumes SET description=? WHERE id=?", description, volumeID)
	return err
}

// Add new storage volume config into database.
func StorageVolumeConfigAdd(tx *sql.Tx, volumeID int64, volumeConfig map[string]string) error {
	str := "INSERT INTO storage_volumes_config (storage_volume_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range volumeConfig {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(volumeID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete storage volume config.
func StorageVolumeConfigClear(tx *sql.Tx, volumeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_volumes_config WHERE storage_volume_id=?", volumeID)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cluster) StorageVolumeCleanupImages(fingerprints []string) error {
	stmt := fmt.Sprintf(
		"DELETE FROM storage_volumes WHERE type=? AND name NOT IN %s",
		query.Params(len(fingerprints)))
	args := []interface{}{StoragePoolVolumeTypeImage}
	for _, fingerprint := range fingerprints {
		args = append(args, fingerprint)
	}
	_, err := exec(c.db, stmt, args...)
	return err
}

func (c *Cluster) StorageVolumeMoveToLVMThinPoolNameKey() error {
	_, err := exec(c.db, "UPDATE storage_pools_config SET key='lvm.thinpool_name' WHERE key='volume.lvm.thinpool_name';")
	if err != nil {
		return err
	}

	_, err = exec(c.db, "DELETE FROM storage_volumes_config WHERE key='lvm.thinpool_name';")
	if err != nil {
		return err
	}

	return nil
}
