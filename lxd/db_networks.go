package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

func dbNetworks(db *sql.DB) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM networks")
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

func dbNetworkGet(db *sql.DB, network string) (int64, *shared.NetworkConfig, error) {
	id := int64(-1)

	q := "SELECT id FROM networks WHERE name=?"
	arg1 := []interface{}{network}
	arg2 := []interface{}{&id}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	config, err := dbNetworkConfigGet(db, id)
	if err != nil {
		return -1, nil, err
	}

	return id, &shared.NetworkConfig{
		Name:    network,
		Managed: true,
		Type:    "bridge",
		Config:  config,
	}, nil
}

func dbNetworkConfigGet(db *sql.DB, id int64) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM networks_config
		WHERE network_id=?`
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}
	results, err := dbQueryScan(db, query, inargs, outfmt)
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
		results, err := dbQueryScan(db, query, []interface{}{id}, []interface{}{r})
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

func dbNetworkCreate(db *sql.DB, name string, config map[string]string) (int64, error) {
	tx, err := dbBegin(db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO networks (name) VALUES (?)", name)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = dbNetworkConfigAdd(tx, id, config)
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

func dbNetworkConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
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

func dbNetworkConfigClear(tx *sql.Tx, id int64) error {
	_, err := tx.Exec("DELETE FROM networks_config WHERE network_id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func dbNetworkDelete(db *sql.DB, name string) error {
	id, _, err := dbNetworkGet(db, name)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM networks WHERE id=?", id)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbNetworkConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

func dbNetworkRename(db *sql.DB, oldName string, newName string) error {
	id, _, err := dbNetworkGet(db, oldName)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE networks SET name=? WHERE id=?", newName, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}
