package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared/api"
)

// Get all storage pools.
func (n *Node) StoragePools() ([]string, error) {
	var name string
	query := "SELECT name FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{name}

	result, err := queryScan(n.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, NoSuchObjectError
	}

	pools := []string{}
	for _, r := range result {
		pools = append(pools, r[0].(string))
	}

	return pools, nil
}

// Get the names of all storage volumes attached to a given storage pool.
func (n *Node) StoragePoolsGetDrivers() ([]string, error) {
	var poolDriver string
	query := "SELECT DISTINCT driver FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{poolDriver}

	result, err := queryScan(n.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, NoSuchObjectError
	}

	drivers := []string{}
	for _, driver := range result {
		drivers = append(drivers, driver[0].(string))
	}

	return drivers, nil
}

// Get id of a single storage pool.
func (n *Node) StoragePoolGetID(poolName string) (int64, error) {
	poolID := int64(-1)
	query := "SELECT id FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID}

	err := dbQueryRowScan(n.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, NoSuchObjectError
		}
	}

	return poolID, nil
}

// Get a single storage pool.
func (n *Node) StoragePoolGet(poolName string) (int64, *api.StoragePool, error) {
	var poolDriver string
	poolID := int64(-1)
	description := sql.NullString{}

	query := "SELECT id, driver, description FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID, &poolDriver, &description}

	err := dbQueryRowScan(n.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, NoSuchObjectError
		}
		return -1, nil, err
	}

	config, err := n.StoragePoolConfigGet(poolID)
	if err != nil {
		return -1, nil, err
	}

	storagePool := api.StoragePool{
		Name:   poolName,
		Driver: poolDriver,
	}
	storagePool.Description = description.String
	storagePool.Config = config

	return poolID, &storagePool, nil
}

// Get config of a storage pool.
func (n *Node) StoragePoolConfigGet(poolID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_pools_config WHERE storage_pool_id=?"
	inargs := []interface{}{poolID}
	outargs := []interface{}{key, value}

	results, err := queryScan(n.db, query, inargs, outargs)
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

// Create new storage pool.
func (n *Node) StoragePoolCreate(poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	tx, err := begin(n.db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO storage_pools (name, description, driver) VALUES (?, ?, ?)", poolName, poolDescription, poolDriver)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = StoragePoolConfigAdd(tx, id, poolConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = TxCommit(tx)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// Add new storage pool config.
func StoragePoolConfigAdd(tx *sql.Tx, poolID int64, poolConfig map[string]string) error {
	str := "INSERT INTO storage_pools_config (storage_pool_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range poolConfig {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(poolID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// Update storage pool.
func (n *Node) StoragePoolUpdate(poolName, description string, poolConfig map[string]string) error {
	poolID, _, err := n.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	tx, err := begin(n.db)
	if err != nil {
		return err
	}

	err = StoragePoolUpdateDescription(tx, poolID, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StoragePoolConfigClear(tx, poolID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StoragePoolConfigAdd(tx, poolID, poolConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Update the storage pool description.
func StoragePoolUpdateDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// Delete storage pool config.
func StoragePoolConfigClear(tx *sql.Tx, poolID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=?", poolID)
	if err != nil {
		return err
	}

	return nil
}

// Delete storage pool.
func (n *Node) StoragePoolDelete(poolName string) (*api.StoragePool, error) {
	poolID, pool, err := n.StoragePoolGet(poolName)
	if err != nil {
		return nil, err
	}

	_, err = exec(n.db, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// Get the names of all storage volumes attached to a given storage pool.
func (n *Node) StoragePoolVolumesGetNames(poolID int64) (int, error) {
	var volumeName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=?"
	inargs := []interface{}{poolID}
	outargs := []interface{}{volumeName}

	result, err := queryScan(n.db, query, inargs, outargs)
	if err != nil {
		return -1, err
	}

	if len(result) == 0 {
		return 0, nil
	}

	return len(result), nil
}

// Get all storage volumes attached to a given storage pool.
func (n *Node) StoragePoolVolumesGet(poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	// Get all storage volumes of all types attached to a given storage
	// pool.
	result := []*api.StorageVolume{}
	for _, volumeType := range volumeTypes {
		volumeNames, err := n.StoragePoolVolumesGetType(volumeType, poolID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		for _, volumeName := range volumeNames {
			_, volume, err := n.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
			if err != nil {
				return nil, err
			}
			result = append(result, volume)
		}
	}

	if len(result) == 0 {
		return result, NoSuchObjectError
	}

	return result, nil
}

// Get all storage volumes attached to a given storage pool of a given volume
// type.
func (n *Node) StoragePoolVolumesGetType(volumeType int, poolID int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND type=?"
	inargs := []interface{}{poolID, volumeType}
	outargs := []interface{}{poolName}

	result, err := queryScan(n.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// Get a single storage volume attached to a given storage pool of a given type.
func (n *Node) StoragePoolVolumeGetType(volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	volumeID, err := n.StoragePoolVolumeGetTypeID(volumeName, volumeType, poolID)
	if err != nil {
		return -1, nil, err
	}

	volumeConfig, err := n.StorageVolumeConfigGet(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeDescription, err := n.StorageVolumeDescriptionGet(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeTypeName, err := StoragePoolVolumeTypeToName(volumeType)
	if err != nil {
		return -1, nil, err
	}

	storageVolume := api.StorageVolume{
		Type: volumeTypeName,
	}
	storageVolume.Name = volumeName
	storageVolume.Description = volumeDescription
	storageVolume.Config = volumeConfig

	return volumeID, &storageVolume, nil
}

// Update storage volume attached to a given storage pool.
func (n *Node) StoragePoolVolumeUpdate(volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	volumeID, _, err := n.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := begin(n.db)
	if err != nil {
		return err
	}

	err = StorageVolumeConfigClear(tx, volumeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StorageVolumeDescriptionUpdate(tx, volumeID, volumeDescription)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Delete storage volume attached to a given storage pool.
func (n *Node) StoragePoolVolumeDelete(volumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := n.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	_, err = exec(n.db, "DELETE FROM storage_volumes WHERE id=?", volumeID)
	if err != nil {
		return err
	}

	return nil
}

// Rename storage volume attached to a given storage pool.
func (n *Node) StoragePoolVolumeRename(oldVolumeName string, newVolumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := n.StoragePoolVolumeGetType(oldVolumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := begin(n.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE storage_volumes SET name=? WHERE id=? AND type=?", newVolumeName, volumeID, volumeType)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Create new storage volume attached to a given storage pool.
func (n *Node) StoragePoolVolumeCreate(volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string) (int64, error) {
	tx, err := begin(n.db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO storage_volumes (storage_pool_id, type, name, description) VALUES (?, ?, ?, ?)",
		poolID, volumeType, volumeName, volumeDescription)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	volumeID, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = StorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = TxCommit(tx)
	if err != nil {
		return -1, err
	}

	return volumeID, nil
}

// Get ID of a storage volume on a given storage pool of a given storage volume
// type.
func (n *Node) StoragePoolVolumeGetTypeID(volumeName string, volumeType int, poolID int64) (int64, error) {
	volumeID := int64(-1)
	query := `SELECT storage_volumes.id
FROM storage_volumes
JOIN storage_pools
ON storage_volumes.storage_pool_id = storage_pools.id
WHERE storage_volumes.storage_pool_id=?
AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{poolID, volumeName, volumeType}
	outargs := []interface{}{&volumeID}

	err := dbQueryRowScan(n.db, query, inargs, outargs)
	if err != nil {
		return -1, NoSuchObjectError
	}

	return volumeID, nil
}

// XXX: this was extracted from lxd/storage_volume_utils.go, we find a way to
//      factor it independently from both the db and main packages.
const (
	StoragePoolVolumeTypeContainer = iota
	StoragePoolVolumeTypeImage
	StoragePoolVolumeTypeCustom
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	StoragePoolVolumeTypeNameContainer string = "container"
	StoragePoolVolumeTypeNameImage     string = "image"
	StoragePoolVolumeTypeNameCustom    string = "custom"
)

// StoragePoolVolumeTypeToName converts a volume integer type code to its
// human-readable name.
func StoragePoolVolumeTypeToName(volumeType int) (string, error) {
	switch volumeType {
	case StoragePoolVolumeTypeContainer:
		return StoragePoolVolumeTypeNameContainer, nil
	case StoragePoolVolumeTypeImage:
		return StoragePoolVolumeTypeNameImage, nil
	case StoragePoolVolumeTypeCustom:
		return StoragePoolVolumeTypeNameCustom, nil
	}

	return "", fmt.Errorf("invalid storage volume type")
}

func (n *Node) StoragePoolInsertZfsDriver() error {
	_, err := exec(n.db, "UPDATE storage_pools SET driver='zfs', description='' WHERE driver=''")
	return err
}
