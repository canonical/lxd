// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// GetNetworksLocalConfig returns a map associating each network name to its
// node-specific config values on the local node (i.e. the ones where node_id
// equals the ID of the local node).
func (c *ClusterTx) GetNetworksLocalConfig() (map[string]map[string]string, error) {
	names, err := query.SelectStrings(c.tx, "SELECT name FROM networks")
	if err != nil {
		return nil, err
	}
	networks := make(map[string]map[string]string, len(names))
	for _, name := range names {
		table := "networks_config JOIN networks ON networks.id=networks_config.network_id"
		config, err := query.SelectConfig(
			c.tx, table, "networks.name=? AND networks_config.node_id=?",
			name, c.nodeID)
		if err != nil {
			return nil, err
		}
		networks[name] = config
	}
	return networks, nil
}

// GetNonPendingNetworkIDs returns a map associating each network name to its ID.
//
// Pending networks are skipped.
func (c *ClusterTx) GetNonPendingNetworkIDs() (map[string]int64, error) {
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
	stmt, err := c.tx.Prepare("SELECT id, name FROM networks WHERE NOT state=?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, networkPending)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, network := range networks {
		ids[network.name] = network.id
	}
	return ids, nil
}

// GetNonPendingNetworks returns a map of api.Network associated to network ID.
//
// Pending networks are skipped.
func (c *ClusterTx) GetNonPendingNetworks() (map[int64]api.Network, error) {
	stmt, err := c.tx.Prepare("SELECT id, name, description, type, state FROM networks WHERE state != ?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(networkPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	networks := make(map[int64]api.Network)

	for i := 0; rows.Next(); i++ {
		var networkID int64
		var networkType NetworkType
		var networkState int
		var network api.Network

		err := rows.Scan(&networkID, &network.Name, &network.Description, &networkType, &networkState)
		if err != nil {
			return nil, err
		}

		// Populate Status and Type fields by converting from DB values.
		networkFillStatus(&network, networkState)
		networkFillType(&network, networkType)

		networks[networkID] = network
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}

	// Populate config.
	for networkID, network := range networks {
		networkConfig, err := query.SelectConfig(c.tx, "networks_config", "network_id=? AND (node_id=? OR node_id IS NULL)", networkID, c.nodeID)
		if err != nil {
			return nil, err
		}

		network.Config = networkConfig
		networks[networkID] = network
	}

	return networks, nil
}

// GetNetworkID returns the ID of the network with the given name.
func (c *ClusterTx) GetNetworkID(name string) (int64, error) {
	stmt := "SELECT id FROM networks WHERE name=?"
	ids, err := query.SelectIntegers(c.tx, stmt, name)
	if err != nil {
		return -1, err
	}
	switch len(ids) {
	case 0:
		return -1, ErrNoSuchObject
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("more than one network has the given name")
	}
}

// CreateNetworkConfig adds a new entry in the networks_config table
func (c *ClusterTx) CreateNetworkConfig(networkID, nodeID int64, config map[string]string) error {
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
	nodes, err := c.GetNodes()
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

// CreatePendingNetwork creates a new pending network on the node with the given name.
func (c *ClusterTx) CreatePendingNetwork(node, name string, netType NetworkType, conf map[string]string) error {
	// First check if a network with the given name exists, and, if so, that it's in the pending state.
	network := struct {
		id      int64
		state   int
		netType NetworkType
	}{}

	var errConsistency error
	dest := func(i int) []interface{} {
		// Sanity check that there is at most one network with the given name.
		if i != 0 {
			errConsistency = fmt.Errorf("More than one network exists with the given name")
		}
		return []interface{}{&network.id, &network.state, &network.netType}
	}

	stmt, err := c.tx.Prepare("SELECT id, state, type FROM networks WHERE name=?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	err = query.SelectObjects(stmt, dest, name)
	if err != nil {
		return err
	}

	if errConsistency != nil {
		return errConsistency
	}

	var networkID = network.id
	if networkID == 0 {
		// No existing network with the given name was found, let's create one.
		columns := []string{"name", "type"}
		values := []interface{}{name, netType}
		networkID, err = query.UpsertObject(c.tx, "networks", columns, values)
		if err != nil {
			return err
		}
	} else {
		// Check that the existing network is in the pending state.
		if network.state != networkPending && network.state != networkErrored {
			return fmt.Errorf("Network is not in pending or errored state")
		}

		// Check that the existing network type matches the requested type.
		if network.netType != netType {
			return fmt.Errorf("Requested network type doesn't match type in existing database record")
		}
	}

	// Get the ID of the node with the given name.
	nodeInfo, err := c.GetNodeByName(node)
	if err != nil {
		return err
	}

	// Check that no network entry for this node and network exists yet.
	count, err := query.Count(c.tx, "networks_nodes", "network_id=? AND node_id=?", networkID, nodeInfo.ID)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrAlreadyDefined
	}

	// Insert the node-specific configuration.
	columns := []string{"network_id", "node_id"}
	values := []interface{}{networkID, nodeInfo.ID}
	_, err = query.UpsertObject(c.tx, "networks_nodes", columns, values)
	if err != nil {
		return err
	}

	err = c.CreateNetworkConfig(networkID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
}

// NetworkCreated sets the state of the given network to "Created".
func (c *ClusterTx) NetworkCreated(name string) error {
	return c.networkState(name, networkCreated)
}

// NetworkErrored sets the state of the given network to "Errored".
func (c *ClusterTx) NetworkErrored(name string) error {
	return c.networkState(name, networkErrored)
}

func (c *ClusterTx) networkState(name string, state int) error {
	stmt := "UPDATE networks SET state=? WHERE name=?"
	result, err := c.tx.Exec(stmt, state, name)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNoSuchObject
	}
	return nil
}

// GetNetworks returns the names of existing networks.
func (c *Cluster) GetNetworks() ([]string, error) {
	return c.networks("")
}

// GetNonPendingNetworks returns the names of all networks that are not pending.
func (c *Cluster) GetNonPendingNetworks() ([]string, error) {
	return c.networks("NOT state=?", networkPending)
}

// Get all networks matching the given WHERE filter (if given).
func (c *Cluster) networks(where string, args ...interface{}) ([]string, error) {
	q := "SELECT name FROM networks"
	inargs := []interface{}{}

	if where != "" {
		q += fmt.Sprintf(" WHERE %s", where)
		for _, arg := range args {
			inargs = append(inargs, arg)
		}
	}

	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(c, q, inargs, outfmt)
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

// NetworkType indicates type of network.
type NetworkType int

// Network types.
const (
	NetworkTypeBridge  NetworkType = iota // Network type bridge.
	NetworkTypeMacvlan                    // Network type macvlan.
	NetworkTypeSriov                      // Network type sriov.
	NetworkTypeOVN                        // Network type ovn.
)

// GetNetworkInAnyState returns the network with the given name.
//
// The network can be in any state.
func (c *Cluster) GetNetworkInAnyState(name string) (int64, *api.Network, error) {
	return c.getNetwork(name, false)
}

// Get the network with the given name. If onlyCreated is true, only return
// networks in the created state.
func (c *Cluster) getNetwork(name string, onlyCreated bool) (int64, *api.Network, error) {
	description := sql.NullString{}
	id := int64(-1)
	state := 0
	var netType NetworkType

	q := "SELECT id, description, state, type FROM networks WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &description, &state, &netType}
	if onlyCreated {
		q += " AND state=?"
		arg1 = append(arg1, networkCreated)
	}
	err := dbQueryRowScan(c, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err
	}

	config, err := c.getNetworkConfig(id)
	if err != nil {
		return -1, nil, err
	}

	network := api.Network{
		Name:    name,
		Managed: true,
	}
	network.Description = description.String
	network.Config = config

	// Populate Status and Type fields by converting from DB values.
	networkFillStatus(&network, state)
	networkFillType(&network, netType)

	nodes, err := c.networkNodes(id)
	if err != nil {
		return -1, nil, err
	}
	network.Locations = nodes

	return id, &network, nil
}

func networkFillStatus(network *api.Network, state int) {
	switch state {
	case networkPending:
		network.Status = api.NetworkStatusPending
	case networkCreated:
		network.Status = api.NetworkStatusCreated
	case networkErrored:
		network.Status = api.NetworkStatusErrored
	default:
		network.Status = api.NetworkStatusUnknown
	}
}

func networkFillType(network *api.Network, netType NetworkType) {
	switch netType {
	case NetworkTypeBridge:
		network.Type = "bridge"
	case NetworkTypeMacvlan:
		network.Type = "macvlan"
	case NetworkTypeSriov:
		network.Type = "sriov"
	case NetworkTypeOVN:
		network.Type = "ovn"
	default:
		network.Type = "" // Unknown
	}
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

// GetNetworkWithInterface returns the network associated with the interface with
// the given name.
func (c *Cluster) GetNetworkWithInterface(devName string) (int64, *api.Network, error) {
	id := int64(-1)
	name := ""
	value := ""

	q := "SELECT networks.id, networks.name, networks_config.value FROM networks LEFT JOIN networks_config ON networks.id=networks_config.network_id WHERE networks_config.key=\"bridge.external_interfaces\" AND networks_config.node_id=?"
	arg1 := []interface{}{c.nodeID}
	arg2 := []interface{}{id, name, value}
	result, err := queryScan(c, q, arg1, arg2)
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

	config, err := c.getNetworkConfig(id)
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

// Return the config map of the network with the given ID.
func (c *Cluster) getNetworkConfig(id int64) (map[string]string, error) {
	var key, value string
	query := `
        SELECT
            key, value
        FROM networks_config
		WHERE network_id=?
                AND (node_id=? OR node_id IS NULL)`
	inargs := []interface{}{id, c.nodeID}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c, query, inargs, outfmt)
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
		results, err := queryScan(c, query, []interface{}{id}, []interface{}{r})
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

// CreateNetwork creates a new network.
func (c *Cluster) CreateNetwork(name, description string, netType NetworkType, config map[string]string) (int64, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		result, err := tx.tx.Exec("INSERT INTO networks (name, description, state, type) VALUES (?, ?, ?, ?)", name, description, networkCreated, netType)
		if err != nil {
			return err
		}

		id, err := result.LastInsertId()
		if err != nil {
			return err
		}

		// Insert a node-specific entry pointing to ourselves.
		columns := []string{"network_id", "node_id"}
		values := []interface{}{id, c.nodeID}
		_, err = query.UpsertObject(tx.tx, "networks_nodes", columns, values)
		if err != nil {
			return err
		}

		err = networkConfigAdd(tx.tx, id, c.nodeID, config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		id = -1
	}
	return id, err
}

// UpdateNetwork updates the network with the given name.
func (c *Cluster) UpdateNetwork(name, description string, config map[string]string) error {
	id, netInfo, err := c.GetNetworkInAnyState(name)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err = updateNetworkDescription(tx.tx, id, description)
		if err != nil {
			return err
		}

		err = clearNetworkConfig(tx.tx, id, c.nodeID)
		if err != nil {
			return err
		}

		err = networkConfigAdd(tx.tx, id, c.nodeID, config)
		if err != nil {
			return err
		}

		// Update network status if change applied successfully.
		if netInfo.Status == api.NetworkStatusErrored {
			err = tx.NetworkCreated(name)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

// Update the description of the network with the given ID.
func updateNetworkDescription(tx *sql.Tx, id int64, description string) error {
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
		if !shared.StringInSlice(k, NodeSpecificNetworkConfig) {
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

// Remove any the config of the network with the given ID
// associated with the node with the given ID.
func clearNetworkConfig(tx *sql.Tx, networkID, nodeID int64) error {
	_, err := tx.Exec(
		"DELETE FROM networks_config WHERE network_id=? AND (node_id=? OR node_id IS NULL)",
		networkID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetwork deletes the network with the given name.
func (c *Cluster) DeleteNetwork(name string) error {
	id, _, err := c.GetNetworkInAnyState(name)
	if err != nil {
		return err
	}

	err = exec(c, "DELETE FROM networks WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetwork renames a network.
func (c *Cluster) RenameNetwork(oldName string, newName string) error {
	id, _, err := c.GetNetworkInAnyState(oldName)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		_, err = tx.tx.Exec("UPDATE networks SET name=? WHERE id=?", newName, id)
		return err
	})

	return err
}

// NodeSpecificNetworkConfig lists all network config keys which are node-specific.
var NodeSpecificNetworkConfig = []string{
	"bridge.external_interfaces",
	"parent",
}
