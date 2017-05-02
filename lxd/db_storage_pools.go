package main

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Get all storage pools.
func dbStoragePools(db *sql.DB) ([]string, error) {
	var name string
	query := "SELECT name FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{name}

	result, err := dbQueryScan(db, query, inargs, outargs)
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
func dbStoragePoolsGetDrivers(db *sql.DB) ([]string, error) {
	var poolDriver string
	query := "SELECT DISTINCT driver FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{poolDriver}

	result, err := dbQueryScan(db, query, inargs, outargs)
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
func dbStoragePoolGetID(db *sql.DB, poolName string) (int64, error) {
	poolID := int64(-1)
	query := "SELECT id FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID}

	err := dbQueryRowScan(db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, NoSuchObjectError
		}
	}

	return poolID, nil
}

// Get a single storage pool.
func dbStoragePoolGet(db *sql.DB, poolName string) (int64, *api.StoragePool, error) {
	var poolDriver string
	poolID := int64(-1)
	description := sql.NullString{}

	query := "SELECT id, driver, description FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID, &poolDriver, &description}

	err := dbQueryRowScan(db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, NoSuchObjectError
		}
		return -1, nil, err
	}

	config, err := dbStoragePoolConfigGet(db, poolID)
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
func dbStoragePoolConfigGet(db *sql.DB, poolID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_pools_config WHERE storage_pool_id=?"
	inargs := []interface{}{poolID}
	outargs := []interface{}{key, value}

	results, err := dbQueryScan(db, query, inargs, outargs)
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
func dbStoragePoolCreate(db *sql.DB, poolName, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	tx, err := dbBegin(db)
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

	err = dbStoragePoolConfigAdd(tx, id, poolConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = txCommit(tx)
	if err != nil {
		return -1, err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheLock.Lock()
	drivers := readStoragePoolDriversCache()
	if !shared.StringInSlice(poolDriver, drivers) {
		drivers = append(drivers, poolDriver)
	}
	storagePoolDriversCacheVal.Store(drivers)
	storagePoolDriversCacheLock.Unlock()

	return id, nil
}

// Add new storage pool config.
func dbStoragePoolConfigAdd(tx *sql.Tx, poolID int64, poolConfig map[string]string) error {
	str := "INSERT INTO storage_pools_config (storage_pool_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(str)
	defer stmt.Close()

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
func dbStoragePoolUpdate(db *sql.DB, poolName, description string, poolConfig map[string]string) error {
	poolID, _, err := dbStoragePoolGet(db, poolName)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	err = dbStoragePoolUpdateDescription(tx, poolID, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbStoragePoolConfigClear(tx, poolID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbStoragePoolConfigAdd(tx, poolID, poolConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

// Update the storage pool description.
func dbStoragePoolUpdateDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// Delete storage pool config.
func dbStoragePoolConfigClear(tx *sql.Tx, poolID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=?", poolID)
	if err != nil {
		return err
	}

	return nil
}

// Delete storage pool.
func dbStoragePoolDelete(db *sql.DB, poolName string) error {
	poolID, pool, err := dbStoragePoolGet(db, poolName)
	if err != nil {
		return err
	}

	_, err = dbExec(db, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheLock.Lock()
	drivers := readStoragePoolDriversCache()
	for i := 0; i < len(drivers); i++ {
		if drivers[i] == pool.Driver {
			drivers[i] = drivers[len(drivers)-1]
			drivers[len(drivers)-1] = ""
			drivers = drivers[:len(drivers)-1]
			break
		}
	}
	storagePoolDriversCacheVal.Store(drivers)
	storagePoolDriversCacheLock.Unlock()

	return nil
}

// Get the names of all storage volumes attached to a given storage pool.
func dbStoragePoolVolumesGetNames(db *sql.DB, poolID int64) (int, error) {
	var volumeName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=?"
	inargs := []interface{}{poolID}
	outargs := []interface{}{volumeName}

	result, err := dbQueryScan(db, query, inargs, outargs)
	if err != nil {
		return -1, err
	}

	if len(result) == 0 {
		return 0, nil
	}

	return len(result), nil
}

// Get all storage volumes attached to a given storage pool.
func dbStoragePoolVolumesGet(db *sql.DB, poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	// Get all storage volumes of all types attached to a given storage
	// pool.
	result := []*api.StorageVolume{}
	for _, volumeType := range volumeTypes {
		volumeNames, err := dbStoragePoolVolumesGetType(db, volumeType, poolID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		for _, volumeName := range volumeNames {
			_, volume, err := dbStoragePoolVolumeGetType(db, volumeName, volumeType, poolID)
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
func dbStoragePoolVolumesGetType(db *sql.DB, volumeType int, poolID int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND type=?"
	inargs := []interface{}{poolID, volumeType}
	outargs := []interface{}{poolName}

	result, err := dbQueryScan(db, query, inargs, outargs)
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
func dbStoragePoolVolumeGetType(db *sql.DB, volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	volumeID, err := dbStoragePoolVolumeGetTypeID(db, volumeName, volumeType, poolID)
	if err != nil {
		return -1, nil, err
	}

	volumeConfig, err := dbStorageVolumeConfigGet(db, volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeDescription, err := dbStorageVolumeDescriptionGet(db, volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeTypeName, err := storagePoolVolumeTypeToName(volumeType)
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
func dbStoragePoolVolumeUpdate(db *sql.DB, volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	volumeID, _, err := dbStoragePoolVolumeGetType(db, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	err = dbStorageVolumeConfigClear(tx, volumeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbStorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbStorageVolumeDescriptionUpdate(tx, volumeID, volumeDescription)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

// Delete storage volume attached to a given storage pool.
func dbStoragePoolVolumeDelete(db *sql.DB, volumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := dbStoragePoolVolumeGetType(db, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	_, err = dbExec(db, "DELETE FROM storage_volumes WHERE id=?", volumeID)
	if err != nil {
		return err
	}

	return nil
}

// Rename storage volume attached to a given storage pool.
func dbStoragePoolVolumeRename(db *sql.DB, oldVolumeName string, newVolumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := dbStoragePoolVolumeGetType(db, oldVolumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE storage_volumes SET name=? WHERE id=? AND type=?", newVolumeName, volumeID, volumeType)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

// Create new storage volume attached to a given storage pool.
func dbStoragePoolVolumeCreate(db *sql.DB, volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string) (int64, error) {
	tx, err := dbBegin(db)
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

	err = dbStorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = txCommit(tx)
	if err != nil {
		return -1, err
	}

	return volumeID, nil
}

// Get ID of a storage volume on a given storage pool of a given storage volume
// type.
func dbStoragePoolVolumeGetTypeID(db *sql.DB, volumeName string, volumeType int, poolID int64) (int64, error) {
	volumeID := int64(-1)
	query := `SELECT storage_volumes.id
FROM storage_volumes
JOIN storage_pools
ON storage_volumes.storage_pool_id = storage_pools.id
WHERE storage_volumes.storage_pool_id=?
AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{poolID, volumeName, volumeType}
	outargs := []interface{}{&volumeID}

	err := dbQueryRowScan(db, query, inargs, outargs)
	if err != nil {
		return -1, NoSuchObjectError
	}

	return volumeID, nil
}
