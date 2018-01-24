package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
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
		config, err := query.SelectConfig(c.tx, table, "networks.name=?", name)
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

// NetworkID returns the ID of the network with the given name.
func (c *ClusterTx) NetworkID(name string) (int64, error) {
	stmt := "SELECT id FROM networks WHERE name=?"
	ids, err := query.SelectIntegers(c.tx, stmt, name)
	if err != nil {
		return -1, err
	}
	switch len(ids) {
	case 0:
		return -1, NoSuchObjectError
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("more than one network has the given name")
	}
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

// NetworkNodeConfigs returns the node-specific configuration of all
// nodes grouped by node name, for the given networkID.
//
// If the network is not defined on all nodes, an error is returned.
func (c *ClusterTx) NetworkNodeConfigs(networkID int64) (map[string]map[string]string, error) {
	// Fetch all nodes.
	nodes, err := c.Nodes()
	if err != nil {
		return nil, err
	}

	// Fetch the names of the nodes where the storage network is defined.
	stmt := `
SELECT nodes.name FROM nodes
  LEFT JOIN networks_nodes ON networks_nodes.node_id = nodes.id
  LEFT JOIN networks ON networks_nodes.network_id = networks.id
WHERE networks.id = ? AND networks.state = ?
`
	defined, err := query.SelectStrings(c.tx, stmt, networkID, networkPending)
	if err != nil {
		return nil, err
	}

	// Figure which nodes are missing
	missing := []string{}
	for _, node := range nodes {
		if !shared.StringInSlice(node.Name, defined) {
			missing = append(missing, node.Name)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("Network not defined on nodes: %s", strings.Join(missing, ", "))
	}

	configs := map[string]map[string]string{}
	for _, node := range nodes {
		config, err := query.SelectConfig(c.tx, "networks_config", "node_id=?", node.ID)
		if err != nil {
			return nil, err
		}
		configs[node.Name] = config
	}

	return configs, nil
}

// NetworkCreatePending creates a new pending network on the node with
// the given name.
func (c *ClusterTx) NetworkCreatePending(node, name string, conf map[string]string) error {
	// First check if a network with the given name exists, and, if
	// so, that it's in the pending state.
	network := struct {
		id    int64
		state int
	}{}

	var errConsistency error
	dest := func(i int) []interface{} {
		// Sanity check that there is at most one pool with the given name.
		if i != 0 {
			errConsistency = fmt.Errorf("more than one network exists with the given name")
		}
		return []interface{}{&network.id, &network.state}
	}
	stmt := "SELECT id, state FROM networks WHERE name=?"
	err := query.SelectObjects(c.tx, dest, stmt, name)
	if err != nil {
		return err
	}
	if errConsistency != nil {
		return errConsistency
	}

	var networkID = network.id
	if networkID == 0 {
		// No existing network with the given name was found, let's create
		// one.
		columns := []string{"name"}
		values := []interface{}{name}
		networkID, err = query.UpsertObject(c.tx, "networks", columns, values)
		if err != nil {
			return err
		}
	} else {
		// Check that the existing network  is in the pending state.
		if network.state != networkPending {
			return fmt.Errorf("network is not in pending state")
		}
	}

	// Get the ID of the node with the given name.
	nodeInfo, err := c.NodeByName(node)
	if err != nil {
		return err
	}

	// Check that no network entry for this node and network exists yet.
	count, err := query.Count(
		c.tx, "networks_nodes", "network_id=? AND node_id=?", networkID, nodeInfo.ID)
	if err != nil {
		return err
	}
	if count != 0 {
		return DbErrAlreadyDefined
	}

	// Insert the node-specific configuration.
	columns := []string{"network_id", "node_id"}
	values := []interface{}{networkID, nodeInfo.ID}
	_, err = query.UpsertObject(c.tx, "networks_nodes", columns, values)
	if err != nil {
		return err
	}
	err = c.NetworkConfigAdd(networkID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
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

// Network state.
const (
	networkPending int = iota // Network defined but not yet created.
	networkCreated            // Network created on all nodes.
	networkErrored            // Network creation failed on some nodes
)

func (c *Cluster) NetworkGet(name string) (int64, *api.Network, error) {
	description := sql.NullString{}
	id := int64(-1)
	state := 0

	q := "SELECT id, description, state FROM networks WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description, &state}
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

	switch state {
	case networkPending:
		network.State = "PENDING"
	case networkCreated:
		network.State = "CREATED"
	default:
		network.State = "UNKNOWN"
	}

	nodes, err := c.networkNodes(id)
	if err != nil {
		return -1, nil, err
	}
	network.Nodes = nodes

	return id, &network, nil
}

// Return the names of the nodes the given network is defined on.
func (c *Cluster) networkNodes(networkID int64) ([]string, error) {
	stmt := `
SELECT nodes.name FROM nodes
  JOIN networks_nodes ON networks_nodes.node_id = nodes.id
  WHERE networks_nodes.network_id = ?
`
	var nodes []string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		nodes, err = query.SelectStrings(tx.tx, stmt, networkID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return nodes, nil
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
                AND (node_id=? OR node_id IS NULL)`
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

	result, err := tx.Exec("INSERT INTO networks (name, description, state) VALUES (?, ?, ?)", name, description, networkCreated)
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
		var nodeIDValue interface{}
		if k != "bridge.external_interfaces" {
			nodeIDValue = nil
		} else {
			nodeIDValue = nodeID
		}

		_, err = stmt.Exec(networkID, nodeIDValue, k, v)
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
