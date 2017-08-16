package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared/api"
)

func Networks(db *sql.DB) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM networks")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := QueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

func NetworkGet(db *sql.DB, name string) (int64, *api.Network, error) {
	description := sql.NullString{}
	id := int64(-1)

	q := "SELECT id, description FROM networks WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	config, err := NetworkConfigGet(db, id)
	if err != nil {
		return -1, nil, err
	}

	network := api.Network{
		Name:    name,
		Managed: true,
		Type:    "bridge",
	}
	network.Description = description.String
	network.Config = config

	return id, &network, nil
}

func NetworkGetInterface(db *sql.DB, devName string) (int64, *api.Network, error) {
	id := int64(-1)
	name := ""
	value := ""

	q := "SELECT networks.id, networks.name, networks_config.value FROM networks LEFT JOIN networks_config ON networks.id=networks_config.network_id WHERE networks_config.key=\"bridge.external_interfaces\""
	arg1 := []interface{}{}
	arg2 := []interface{}{id, name, value}
	result, err := QueryScan(db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	for _, r := range result {
		for _, entry := range strings.Split(r[2].(string), ",") {
			entry = strings.TrimSpace(entry)

			if entry == devName {
				id = r[0].(int64)
				name = r[1].(string)
			}
		}
	}

	if id == -1 {
		return -1, nil, fmt.Errorf("No network found for interface: %s", devName)
	}

	config, err := NetworkConfigGet(db, id)
	if err != nil {
		return -1, nil, err
	}

	network := api.Network{
		Name:    name,
		Managed: true,
		Type:    "bridge",
	}
	network.Config = config

	return id, &network, nil
}

func NetworkConfigGet(db *sql.DB, id int64) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM networks_config
		WHERE network_id=?`
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}
	results, err := QueryScan(db, query, inargs, outfmt)
	if err != nil {
		return nil, fmt.Errorf("Failed to get network '%d'", id)
	}

	if len(results) == 0 {
		/*
		 * If we didn't get any rows here, let's check to make sure the
		 * network really exists; if it doesn't, let's send back a 404.
		 */
		query := "SELECT id FROM networks WHERE id=?"
		var r int
		results, err := QueryScan(db, query, []interface{}{id}, []interface{}{r})
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

func NetworkCreate(db *sql.DB, name, description string, config map[string]string) (int64, error) {
	tx, err := Begin(db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO networks (name, description) VALUES (?, ?)", name, description)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = NetworkConfigAdd(tx, id, config)
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

func NetworkUpdate(db *sql.DB, name, description string, config map[string]string) error {
	id, _, err := NetworkGet(db, name)
	if err != nil {
		return err
	}

	tx, err := Begin(db)
	if err != nil {
		return err
	}

	err = NetworkUpdateDescription(tx, id, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = NetworkConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = NetworkConfigAdd(tx, id, config)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

func NetworkUpdateDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE networks SET description=? WHERE id=?", description, id)
	return err
}

func NetworkConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO networks_config (network_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

func NetworkConfigClear(tx *sql.Tx, id int64) error {
	_, err := tx.Exec("DELETE FROM networks_config WHERE network_id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func NetworkDelete(db *sql.DB, name string) error {
	id, _, err := NetworkGet(db, name)
	if err != nil {
		return err
	}

	_, err = Exec(db, "DELETE FROM networks WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func NetworkRename(db *sql.DB, oldName string, newName string) error {
	id, _, err := NetworkGet(db, oldName)
	if err != nil {
		return err
	}

	tx, err := Begin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE networks SET name=? WHERE id=?", newName, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}
