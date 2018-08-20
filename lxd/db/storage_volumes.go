package db

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
)

// StorageVolumeArgs is a value object holding all db-related details about a
// storage volume.
type StorageVolumeArgs struct {
	Name string

	// At least one of Type or TypeName must be set.
	Type     int
	TypeName string

	// At least one of PoolID or PoolName must be set.
	PoolID   int64
	PoolName string

	Kind StorageVolumeKind

	Config       map[string]string
	Description  string
	CreationDate time.Time
}

// StorageVolumeKind encodes the type of storage volume (either regular or snapshot).
type StorageVolumeKind int

// Numerical codes for storage volume types.
const (
	StorageVolumeKindValid    StorageVolumeKind = 0
	StorageVolumeKindRegular  StorageVolumeKind = 0
	StorageVolumeKindSnapshot StorageVolumeKind = 1
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
		return nil, ErrNoSuchObject
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
			return "", ErrNoSuchObject
		}
		return "", err
	}

	return name, nil
}

// StorageVolumeConfigGet gets the config of a storage volume.
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

// StorageVolumeDescriptionGet gets the description of a storage volume.
func (c *Cluster) StorageVolumeDescriptionGet(volumeID int64) (string, error) {
	description := sql.NullString{}
	query := "SELECT description FROM storage_volumes WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&description}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNoSuchObject
		}
		return "", err
	}

	return description.String, nil
}

// StorageVolumeDescriptionUpdate updates the description of a storage volume.
func StorageVolumeDescriptionUpdate(tx *sql.Tx, volumeID int64, description string) error {
	_, err := tx.Exec("UPDATE storage_volumes SET description=? WHERE id=?", description, volumeID)
	return err
}

// StorageVolumeConfigAdd adds a new storage volume config into database.
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

// StorageVolumeConfigClear deletes storage volume config.
func StorageVolumeConfigClear(tx *sql.Tx, volumeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_volumes_config WHERE storage_volume_id=?", volumeID)
	if err != nil {
		return err
	}

	return nil
}

// Get the IDs of all volumes with the given name and type associated with the
// given pool, regardless of their node_id column.
func storageVolumeIDsGet(tx *sql.Tx, volumeName string, volumeType int, poolID int64) ([]int64, error) {
	ids, err := query.SelectIntegers(tx, `
SELECT id FROM storage_volumes WHERE name=? AND type=? AND storage_pool_id=?
`, volumeName, volumeType, poolID)
	if err != nil {
		return nil, err
	}
	ids64 := make([]int64, len(ids))
	for i, id := range ids {
		ids64[i] = int64(id)
	}
	return ids64, nil
}

// StorageVolumeCleanupImages removes the volumes with the given fingerprints.
func (c *Cluster) StorageVolumeCleanupImages(fingerprints []string) error {
	stmt := fmt.Sprintf(
		"DELETE FROM storage_volumes WHERE type=? AND name NOT IN %s",
		query.Params(len(fingerprints)))
	args := []interface{}{StoragePoolVolumeTypeImage}
	for _, fingerprint := range fingerprints {
		args = append(args, fingerprint)
	}
	err := exec(c.db, stmt, args...)
	return err
}

// StorageVolumeMoveToLVMThinPoolNameKey upgrades the config keys of LVM
// volumes.
func (c *Cluster) StorageVolumeMoveToLVMThinPoolNameKey() error {
	err := exec(c.db, "UPDATE storage_pools_config SET key='lvm.thinpool_name' WHERE key='volume.lvm.thinpool_name';")
	if err != nil {
		return err
	}

	err = exec(c.db, "DELETE FROM storage_volumes_config WHERE key='lvm.thinpool_name';")
	if err != nil {
		return err
	}

	return nil
}
