package db

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared/api"
)

// Profiles returns a string list of profiles.
func (c *Cluster) Profiles() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM profiles")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// ProfileGet returns the profile with the given name.
func (c *Cluster) ProfileGet(name string) (int64, *api.Profile, error) {
	id := int64(-1)
	description := sql.NullString{}

	q := "SELECT id, description FROM profiles WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err
	}

	config, err := c.ProfileConfig(name)
	if err != nil {
		return -1, nil, err
	}

	devices, err := c.Devices(name, true)
	if err != nil {
		return -1, nil, err
	}

	profile := api.Profile{
		Name: name,
	}

	profile.Config = config
	profile.Description = description.String
	profile.Devices = devices

	return id, &profile, nil
}

// ProfileCreate creates a new profile.
func (c *Cluster) ProfileCreate(profile string, description string, config map[string]string,
	devices types.Devices) (int64, error) {

	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		result, err := tx.tx.Exec("INSERT INTO profiles (name, description, project_id) VALUES (?, ?, 1)", profile, description)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}

		err = ProfileConfigAdd(tx.tx, id, config)
		if err != nil {
			return err
		}

		err = DevicesAdd(tx.tx, "profile", id, devices)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		id = -1
	}

	return id, nil
}

// ProfileCreateDefault creates the default profile.
func (c *Cluster) ProfileCreateDefault() error {
	id, _, _ := c.ProfileGet("default")

	if id != -1 {
		// default profile already exists
		return nil
	}

	_, err := c.ProfileCreate("default", "Default LXD profile", map[string]string{}, types.Devices{})
	if err != nil {
		return err
	}

	return nil
}

// ProfileConfig gets the profile configuration map from the DB.
func (c *Cluster) ProfileConfig(name string) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM profiles_config
        JOIN profiles ON profiles_config.profile_id=profiles.id
		WHERE name=?`
	inargs := []interface{}{name}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c.db, query, inargs, outfmt)
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
		results, err := queryScan(c.db, query, []interface{}{name}, []interface{}{id})
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			return nil, ErrNoSuchObject
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

// ProfileDelete deletes the profile with the given name.
func (c *Cluster) ProfileDelete(name string) error {
	id, _, err := c.ProfileGet(name)
	if err != nil {
		return err
	}

	err = exec(c.db, "DELETE FROM profiles WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// ProfileUpdate renames the profile with the given name to the given new name.
func (c *Cluster) ProfileUpdate(name string, newName string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE profiles SET name=? WHERE name=?", newName, name)
		return err
	})
	return err
}

// ProfileDescriptionUpdate updates the description of the profile with the given ID.
func ProfileDescriptionUpdate(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE profiles SET description=? WHERE id=?", description, id)
	return err
}

// ProfileConfigClear resets the config of the profile with the given ID.
func ProfileConfigClear(tx *sql.Tx, id int64) error {
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

// ProfileConfigAdd adds a config to the profile with the given ID.
func ProfileConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO profiles_config (profile_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range config {
		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// ProfileContainersGet gets the names of the containers associated with the
// profile with the given name.
func (c *Cluster) ProfileContainersGet(profile string) ([]string, error) {
	q := `SELECT containers.name FROM containers JOIN containers_profiles
		ON containers.id == containers_profiles.container_id
		JOIN profiles ON containers_profiles.profile_id == profiles.id
		WHERE profiles.name == ? AND containers.type == 0`

	results := []string{}
	inargs := []interface{}{profile}
	var name string
	outfmt := []interface{}{name}

	output, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return results, err
	}

	for _, r := range output {
		results = append(results, r[0].(string))
	}

	return results, nil
}

// ProfileCleanupLeftover removes unreferenced profiles.
func (c *Cluster) ProfileCleanupLeftover() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`
	err := exec(c.db, stmt)
	if err != nil {
		return err
	}

	return nil
}
