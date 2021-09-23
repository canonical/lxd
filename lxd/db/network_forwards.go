//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// CreateNetworkForward creates a new Network Forward.
// If memberSpecific is true, then the forward is associated to the current member, rather than being associated to
// all members.
func (c *Cluster) CreateNetworkForward(networkID int64, memberSpecific bool, info *api.NetworkForwardsPost) (int64, error) {
	var err error
	var forwardID int64
	var nodeID interface{}

	if memberSpecific {
		nodeID = c.nodeID
	}

	var portsJSON []byte

	if info.Ports != nil {
		portsJSON, err = json.Marshal(info.Ports)
		if err != nil {
			return -1, fmt.Errorf("Failed marshalling ports: %w", err)
		}
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		// Insert a new Network forward record.
		result, err := tx.tx.Exec(`
		INSERT INTO networks_forwards
		(network_id, node_id, listen_address, description, ports)
		VALUES (?, ?, ?, ?, ?)
		`, networkID, nodeID, info.ListenAddress, info.Description, string(portsJSON))
		if err != nil {
			return err
		}

		forwardID, err = result.LastInsertId()
		if err != nil {
			return err
		}

		// Save config.
		err = networkForwardConfigAdd(tx.tx, forwardID, info.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return -1, err
	}

	return forwardID, err
}

// networkForwardConfigAdd inserts Network forward config keys.
func networkForwardConfigAdd(tx *sql.Tx, forwardID int64, config map[string]string) error {
	stmt, err := tx.Prepare(`
	INSERT INTO networks_forwards_config
	(network_forward_id, key, value)
	VALUES(?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(forwardID, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateNetworkForward updates an existing Network Forward.
func (c *Cluster) UpdateNetworkForward(networkID int64, forwardID int64, info *api.NetworkForwardPut) error {
	var err error
	var portsJSON []byte

	if info.Ports != nil {
		portsJSON, err = json.Marshal(info.Ports)
		if err != nil {
			return fmt.Errorf("Failed marshalling ports: %w", err)
		}
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		// Update existing Network forward record.
		res, err := tx.tx.Exec(`
		UPDATE networks_forwards
		SET description = ?, ports = ?
		WHERE network_id = ? and id = ?
		`, info.Description, string(portsJSON), networkID, forwardID)
		if err != nil {
			return err
		}

		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return err
		}

		if rowsAffected <= 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
		}

		// Save config.
		_, err = tx.tx.Exec("DELETE FROM networks_forwards_config WHERE network_forward_id=?", forwardID)
		if err != nil {
			return err
		}

		err = networkForwardConfigAdd(tx.tx, forwardID, info.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkForward deletes an existing Network Forward.
func (c *Cluster) DeleteNetworkForward(networkID int64, forwardID int64) error {
	return c.Transaction(func(tx *ClusterTx) error {
		// Delete existing Network forward record.
		res, err := tx.tx.Exec(`
			DELETE FROM networks_forwards
			WHERE network_id = ? and id = ?
		`, networkID, forwardID)
		if err != nil {
			return err
		}

		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return err
		}

		if rowsAffected <= 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
		}

		return nil
	})
}

// GetNetworkForward returns the Network Forward ID and info for the given network ID and listen address.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members.
func (c *Cluster) GetNetworkForward(networkID int64, memberSpecific bool, listenAddress string) (int64, *api.NetworkForward, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []interface{}{networkID, listenAddress}

	q.WriteString(`
	SELECT
		IFNULL(networks_forwards.id, -1) ,
		IFNULL(networks_forwards.listen_address, ""),
		IFNULL(networks_forwards.description, ""),
		IFNULL(nodes.name, "") as location,
		IFNULL(networks_forwards.ports, ""),
		COUNT(networks_forwards.id) as rowCount
	FROM networks_forwards
	LEFT JOIN nodes ON nodes.id = networks_forwards.node_id
	WHERE networks_forwards.network_id = ? AND networks_forwards.listen_address = ?
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ? OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	var err error
	var forwardID int64 = int64(-1)
	var forward api.NetworkForward
	var portsJSON string

	err = c.Transaction(func(tx *ClusterTx) error {
		var rowCount int

		err = tx.tx.QueryRow(q.String(), args...).Scan(&forwardID, &forward.ListenAddress, &forward.Description, &forward.Location, &portsJSON, &rowCount)
		if rowCount <= 0 || errors.Is(err, sql.ErrNoRows) {
			return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
		} else if rowCount > 1 {
			return api.StatusErrorf(http.StatusConflict, "Network forward found on more than one cluster member. Please target a specific member")
		} else if err != nil {
			return err
		}

		err = networkForwardConfig(tx, forwardID, &forward)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return -1, nil, err
	}

	forward.Ports = []api.NetworkForwardPort{}
	if portsJSON != "" {
		err = json.Unmarshal([]byte(portsJSON), &forward.Ports)
		if err != nil {
			return -1, nil, fmt.Errorf("Failed unmarshalling ports: %w", err)
		}
	}

	return forwardID, &forward, nil
}

// networkForwardConfig populates the config map of the Network Forward with the given ID.
func networkForwardConfig(tx *ClusterTx, forwardID int64, forward *api.NetworkForward) error {
	q := `
	SELECT
		key,
		value
	FROM networks_forwards_config
	WHERE network_forward_id=?
	`

	forward.Config = make(map[string]string)
	return tx.QueryScan(q, func(scan func(dest ...interface{}) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := forward.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network forward ID %d", key, forwardID)
		}

		forward.Config[key] = value

		return nil
	}, forwardID)
}

// GetNetworkForwardListenAddresses returns map of Network Forward Listen Addresses for the given network ID keyed
// on Forward ID.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members.
func (c *Cluster) GetNetworkForwardListenAddresses(networkID int64, memberSpecific bool) (map[int64]string, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []interface{}{networkID}

	q.WriteString(`
	SELECT
		id,
		listen_address
	FROM networks_forwards
	WHERE networks_forwards.network_id = ?
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ? OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	forwards := make(map[int64]string)

	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.QueryScan(q.String(), func(scan func(dest ...interface{}) error) error {
			var forwardID int64 = int64(-1)
			var listenAddress string

			err := scan(&forwardID, &listenAddress)
			if err != nil {
				return err
			}

			forwards[forwardID] = listenAddress

			return nil
		}, args...)
	})
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetProjectNetworkForwardListenAddressesByUplink returns map of Network Forward Listen Addresses that belong to
// networks connected to the specified uplinkNetworkName.
// Returns a map keyed on project name and network ID containing a slice of listen addresses.
func (c *ClusterTx) GetProjectNetworkForwardListenAddressesByUplink(uplinkNetworkName string) (map[string]map[int64][]string, error) {
	// As uplink networks can only be in default project, it is safe to look for networks that reference the
	// specified uplinkNetworkName in their "network" config property.
	q := `
	SELECT
		projects.name,
		networks.id,
		networks_forwards.listen_address
	FROM networks_forwards
	JOIN networks on networks.id = networks_forwards.network_id
	JOIN networks_config on networks.id = networks_config.network_id
	JOIN projects ON projects.id = networks.project_id
	WHERE networks_config.key = "network"
	AND networks_config.value = ?
	`
	forwards := make(map[string]map[int64][]string)

	err := c.QueryScan(q, func(scan func(dest ...interface{}) error) error {
		var projectName string
		var networkID int64 = int64(-1)
		var listenAddress string

		err := scan(&projectName, &networkID, &listenAddress)
		if err != nil {
			return err
		}

		if forwards[projectName] == nil {
			forwards[projectName] = make(map[int64][]string)
		}

		if forwards[projectName][networkID] == nil {
			forwards[projectName][networkID] = make([]string, 0)
		}

		forwards[projectName][networkID] = append(forwards[projectName][networkID], listenAddress)

		return nil
	}, uplinkNetworkName)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetProjectNetworkForwardListenAddressesOnMember returns map of Network Forward Listen Addresses that belong to
// to this specific cluster member. Will not include forwards that do not have a specific member.
// Returns a map keyed on project name and network ID containing a slice of listen addresses.
func (c *ClusterTx) GetProjectNetworkForwardListenAddressesOnMember() (map[string]map[int64][]string, error) {
	q := `
	SELECT
		projects.name,
		networks.id,
		networks_forwards.listen_address
	FROM networks_forwards
	JOIN networks on networks.id = networks_forwards.network_id
	JOIN projects ON projects.id = networks.project_id
	WHERE networks_forwards.node_id = ?
	`
	forwards := make(map[string]map[int64][]string)

	err := c.QueryScan(q, func(scan func(dest ...interface{}) error) error {
		var projectName string
		var networkID int64 = int64(-1)
		var listenAddress string

		err := scan(&projectName, &networkID, &listenAddress)
		if err != nil {
			return err
		}

		if forwards[projectName] == nil {
			forwards[projectName] = make(map[int64][]string)
		}

		if forwards[projectName][networkID] == nil {
			forwards[projectName][networkID] = make([]string, 0)
		}

		forwards[projectName][networkID] = append(forwards[projectName][networkID], listenAddress)

		return nil
	}, c.nodeID)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetNetworkForwards returns map of Network Forwards for the given network ID keyed on Forward ID.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members.
func (c *Cluster) GetNetworkForwards(networkID int64, memberSpecific bool) (map[int64]*api.NetworkForward, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []interface{}{networkID}

	q.WriteString(`
	SELECT
		networks_forwards.id,
		networks_forwards.listen_address,
		networks_forwards.description,
		IFNULL(nodes.name, "") as location,
		networks_forwards.ports
	FROM networks_forwards
	LEFT JOIN nodes ON nodes.id = networks_forwards.node_id
	WHERE networks_forwards.network_id = ?
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ? OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	var err error
	forwards := make(map[int64]*api.NetworkForward)

	err = c.Transaction(func(tx *ClusterTx) error {
		err = tx.QueryScan(q.String(), func(scan func(dest ...interface{}) error) error {
			var forwardID int64 = int64(-1)
			var portsJSON string
			var forward api.NetworkForward

			err := scan(&forwardID, &forward.ListenAddress, &forward.Description, &forward.Location, &portsJSON)
			if err != nil {
				return err
			}

			forward.Ports = []api.NetworkForwardPort{}
			if portsJSON != "" {
				err = json.Unmarshal([]byte(portsJSON), &forward.Ports)
				if err != nil {
					return fmt.Errorf("Failed unmarshalling ports: %w", err)
				}
			}

			forwards[forwardID] = &forward

			return nil
		}, args...)
		if err != nil {
			return err
		}

		// Populate config.
		for forwardID := range forwards {
			err = networkForwardConfig(tx, forwardID, forwards[forwardID])
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return forwards, nil
}
