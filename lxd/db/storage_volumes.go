package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// Get config of a storage volume.
func StorageVolumeConfigGet(db *sql.DB, volumeID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_volumes_config WHERE storage_volume_id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{key, value}

	results, err := QueryScan(db, query, inargs, outargs)
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
func StorageVolumeDescriptionGet(db *sql.DB, volumeID int64) (string, error) {
	description := sql.NullString{}
	query := "SELECT description FROM storage_volumes WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&description}

	err := dbQueryRowScan(db, query, inargs, outargs)
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
