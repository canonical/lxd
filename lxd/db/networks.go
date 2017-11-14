package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
)

// NetworkConfigs returns a map associating each network name to its config
// values.
func (c *ClusterTx) NetworkConfigs() (map[string]map[string]string, error) {
	names, err := query.SelectStrings(c.tx, "SELECT name FROM networks")
	if err != nil {
		return nil, err
	}
	networks := make(map[string]map[string]string, len(names))
	for _, name := range names {
		table := "networks_config JOIN networks ON networks.id=networks_config.network_id"
		config, err := query.SelectConfig(c.tx, table, fmt.Sprintf("networks.name='%s'", name))
		if err != nil {
			return nil, err
		}
		networks[name] = config
	}
	return networks, nil
}

// NetworkIDs returns a map associating each network name to its ID.
func (c *ClusterTx) NetworkIDs() (map[string]int64, error) {
	networks := []struct {
		id   int64
		name string
	}{}
	dest := func(i int) []interface{} {
		networks = append(networks, struct {
			id   int64
			name string
		}{})
		return []interface{}{&networks[i].id, &networks[i].name}

	}
	err := query.SelectObjects(c.tx, dest, "SELECT id, name FROM networks")
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, network := range networks {
		ids[network.name] = network.id
	}
	return ids, nil
}

// NetworkConfigAdd adds a new entry in the networks_config table
func (c *ClusterTx) NetworkConfigAdd(networkID, nodeID int64, config map[string]string) error {
	return networkConfigAdd(c.tx, networkID, nodeID, config)
}

// NetworkNodeJoin adds a new entry in the networks_nodes table.
//
// It should only be used when a new node joins the cluster, when it's safe to
// assume that the relevant network has already been created on the joining node,
// and we just need to track it.
func (c *ClusterTx) NetworkNodeJoin(networkID, nodeID int64) error {
	columns := []string{"network_id", "node_id"}
	values := []interface{}{networkID, nodeID}
	_, err := query.UpsertObject(c.tx, "networks_nodes", columns, values)
	return err
}

func (c *Cluster) Networks() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM networks")
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

func (c *Cluster) NetworkGet(name string) (int64, *api.Network, error) {
	description := sql.NullString{}
	id := int64(-1)

	q := "SELECT id, description FROM networks WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		return -1, nil, err
	}

	config, err := c.NetworkConfigGet(id)
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

func (c *Cluster) NetworkGetInterface(devName string) (int64, *api.Network, error) {
	id := int64(-1)
	name := ""
	value := ""

	q := "SELECT networks.id, networks.name, networks_config.value FROM networks LEFT JOIN networks_config ON networks.id=networks_config.network_id WHERE networks_config.key=\"bridge.external_interfaces\" AND networks_config.node_id=?"
	arg1 := []interface{}{c.nodeID}
	arg2 := []interface{}{id, name, value}
	result, err := queryScan(c.db, q, arg1, arg2)
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

	config, err := c.NetworkConfigGet(id)
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

func (c *Cluster) NetworkConfigGet(id int64) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM networks_config
		WHERE network_id=?
                AND node_id=?`
	inargs := []interface{}{id, c.nodeID}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c.db, query, inargs, outfmt)
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
		results, err := queryScan(c.db, query, []interface{}{id}, []interface{}{r})
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

func (c *Cluster) NetworkCreate(name, description string, config map[string]string) (int64, error) {
	tx, err := begin(c.db)
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

	// Insert a node-specific entry pointing to ourselves.
	columns := []string{"network_id", "node_id"}
	values := []interface{}{id, c.nodeID}
	_, err = query.UpsertObject(tx, "networks_nodes", columns, values)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = networkConfigAdd(tx, id, c.nodeID, config)
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

func (c *Cluster) NetworkUpdate(name, description string, config map[string]string) error {
	id, _, err := c.NetworkGet(name)
	if err != nil {
		return err
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	err = NetworkUpdateDescription(tx, id, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = NetworkConfigClear(tx, id, c.nodeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = networkConfigAdd(tx, id, c.nodeID, config)
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

func networkConfigAdd(tx *sql.Tx, networkID, nodeID int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO networks_config (network_id, node_id, key, value) VALUES(?, ?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(networkID, nodeID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

func NetworkConfigClear(tx *sql.Tx, networkID, nodeID int64) error {
	_, err := tx.Exec(
		"DELETE FROM networks_config WHERE network_id=? AND node_id=?",
		networkID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cluster) NetworkDelete(name string) error {
	id, _, err := c.NetworkGet(name)
	if err != nil {
		return err
	}

	_, err = exec(c.db, "DELETE FROM networks WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cluster) NetworkRename(oldName string, newName string) error {
	id, _, err := c.NetworkGet(oldName)
	if err != nil {
		return err
	}

	tx, err := begin(c.db)
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
