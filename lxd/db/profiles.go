package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared/api"
)

// Profiles returns a string list of profiles.
func (n *Node) Profiles() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM profiles")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(n.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

func (n *Node) ProfileGet(name string) (int64, *api.Profile, error) {
	id := int64(-1)
	description := sql.NullString{}

	q := "SELECT id, description FROM profiles WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description}
	err := dbQueryRowScan(n.db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	config, err := n.ProfileConfig(name)
	if err != nil {
		return -1, nil, err
	}

	devices, err := n.Devices(name, true)
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

func (n *Node) ProfileCreate(profile string, description string, config map[string]string,
	devices types.Devices) (int64, error) {

	tx, err := begin(n.db)
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

	err = ProfileConfigAdd(tx, id, config)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = DevicesAdd(tx, "profile", id, devices)
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

func (n *Node) ProfileCreateDefault() error {
	id, _, _ := n.ProfileGet("default")

	if id != -1 {
		// default profile already exists
		return nil
	}

	_, err := n.ProfileCreate("default", "Default LXD profile", map[string]string{}, types.Devices{})
	if err != nil {
		return err
	}

	return nil
}

// Get the profile configuration map from the DB
func (n *Node) ProfileConfig(name string) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM profiles_config
        JOIN profiles ON profiles_config.profile_id=profiles.id
		WHERE name=?`
	inargs := []interface{}{name}
	outfmt := []interface{}{key, value}
	results, err := queryScan(n.db, query, inargs, outfmt)
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
		results, err := queryScan(n.db, query, []interface{}{name}, []interface{}{id})
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

func (n *Node) ProfileDelete(name string) error {
	id, _, err := n.ProfileGet(name)
	if err != nil {
		return err
	}

	_, err = exec(n.db, "DELETE FROM profiles WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func (n *Node) ProfileUpdate(name string, newName string) error {
	tx, err := begin(n.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE profiles SET name=? WHERE name=?", newName, name)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = TxCommit(tx)

	return err
}

func ProfileDescriptionUpdate(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE profiles SET description=? WHERE id=?", description, id)
	return err
}

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

func (n *Node) ProfileContainersGet(profile string) ([]string, error) {
	q := `SELECT containers.name FROM containers JOIN containers_profiles
		ON containers.id == containers_profiles.container_id
		JOIN profiles ON containers_profiles.profile_id == profiles.id
		WHERE profiles.name == ?`

	results := []string{}
	inargs := []interface{}{profile}
	var name string
	outfmt := []interface{}{name}

	output, err := queryScan(n.db, q, inargs, outfmt)
	if err != nil {
		return results, err
	}

	for _, r := range output {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func (n *Node) ProfileCleanupLeftover() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`
	_, err := n.db.Exec(stmt)
	if err != nil {
		return err
	}

	return nil
}
