package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

// dbProfiles returns a string list of profiles.
func dbProfiles(db *sql.DB) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM profiles")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

func dbProfileGet(db *sql.DB, profile string) (int64, *shared.ProfileConfig, error) {
	id := int64(-1)
	description := sql.NullString{}

	q := "SELECT id, description FROM profiles WHERE name=?"
	arg1 := []interface{}{profile}
	arg2 := []interface{}{&id, &description}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	config, err := dbProfileConfig(db, profile)
	if err != nil {
		return -1, nil, err
	}

	devices, err := dbDevices(db, profile, true)
	if err != nil {
		return -1, nil, err
	}

	return id, &shared.ProfileConfig{
		Name:        profile,
		Config:      config,
		Description: description.String,
		Devices:     devices,
	}, nil
}

func dbProfileCreate(db *sql.DB, profile string, description string, config map[string]string,
	devices shared.Devices) (int64, error) {

	tx, err := dbBegin(db)
	if err != nil {
		return -1, err
	}
	result, err := tx.Exec("INSERT INTO profiles (name, description) VALUES (?, ?)", profile, description)
	if err != nil {
		tx.Rollback()
		return -1, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = dbProfileConfigAdd(tx, id, config)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = dbDevicesAdd(tx, "profile", id, devices)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = txCommit(tx)
	if err != nil {
		return -1, err
	}

	return id, nil
}

func dbProfileCreateDefault(db *sql.DB) error {
	id, _, _ := dbProfileGet(db, "default")

	if id != -1 {
		// default profile already exists
		return nil
	}

	id, err := dbProfileCreate(db, "default", "Default LXD profile", map[string]string{}, shared.Devices{})
	if err != nil {
		return err
	}

	return nil
}

func dbProfileCreateDocker(db *sql.DB) error {
	id, _, err := dbProfileGet(db, "docker")

	if id != -1 {
		// docker profile already exists
		return nil
	}

	config := map[string]string{
		"security.nesting":     "true",
		"linux.kernel_modules": "overlay, nf_nat"}
	aadisable := map[string]string{
		"path":   "/sys/module/apparmor/parameters/enabled",
		"type":   "disk",
		"source": "/dev/null",
	}
	devices := map[string]shared.Device{"aadisable": aadisable}

	_, err = dbProfileCreate(db, "docker", "Profile supporting docker in containers", config, devices)
	return err
}

// Get the profile configuration map from the DB
func dbProfileConfig(db *sql.DB, name string) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM profiles_config
        JOIN profiles ON profiles_config.profile_id=profiles.id
		WHERE name=?`
	inargs := []interface{}{name}
	outfmt := []interface{}{key, value}
	results, err := dbQueryScan(db, query, inargs, outfmt)
	if err != nil {
		return nil, fmt.Errorf("Failed to get profile '%s'", name)
	}

	if len(results) == 0 {
		/*
		 * If we didn't get any rows here, let's check to make sure the
		 * profile really exists; if it doesn't, let's send back a 404.
		 */
		query := "SELECT id FROM profiles WHERE name=?"
		var id int
		results, err := dbQueryScan(db, query, []interface{}{name}, []interface{}{id})
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			return nil, NoSuchObjectError
		}
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

func dbProfileDelete(db *sql.DB, name string) error {
	id, _, err := dbProfileGet(db, name)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM profiles WHERE id=?", id)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbProfileConfigClear(tx, id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM containers_profiles WHERE profile_id=?", id)
	if err != nil {
		return err
	}

	return txCommit(tx)
}

func dbProfileUpdate(db *sql.DB, name string, newName string) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE profiles SET name=? WHERE name=?", newName, name)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = txCommit(tx)

	return err
}

func dbProfileDescriptionUpdate(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE profiles SET description=? WHERE id=?", description, id)
	return err
}

func dbProfileConfigClear(tx *sql.Tx, id int64) error {
	_, err := tx.Exec("DELETE FROM profiles_config WHERE profile_id=?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM profiles_devices_config WHERE id IN
		(SELECT profiles_devices_config.id
		 FROM profiles_devices_config JOIN profiles_devices
		 ON profiles_devices_config.profile_device_id=profiles_devices.id
		 WHERE profiles_devices.profile_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM profiles_devices WHERE profile_id=?", id)
	if err != nil {
		return err
	}
	return nil
}

func dbProfileConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO profiles_config (profile_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()

	for k, v := range config {
		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

func dbProfileContainersGet(db *sql.DB, profile string) ([]string, error) {
	q := `SELECT containers.name FROM containers JOIN containers_profiles
		ON containers.id == containers_profiles.container_id
		JOIN profiles ON containers_profiles.profile_id == profiles.id
		WHERE profiles.name == ?`

	results := []string{}
	inargs := []interface{}{profile}
	var name string
	outfmt := []interface{}{name}

	output, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return results, err
	}

	for _, r := range output {
		results = append(results, r[0].(string))
	}

	return results, nil
}
