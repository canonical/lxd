package db

import (
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

// NodeInfo holds information about a single LXD instance in a cluster.
type NodeInfo struct {
	ID            int64  // Stable node identifier
	Name          string // User-assigned name of the node
	Address       string // Network address of the node
	Description   string // Node description (optional)
	Schema        int    // Schema version of the LXD code running the node
	APIExtensions int    // Number of API extensions of the LXD code running on the node
}

// Nodes returns all LXD nodes part of the cluster.
//
// If this LXD instance is not clustered, an empty list is returned.
func (c *ClusterTx) Nodes() ([]NodeInfo, error) {
	nodes := []NodeInfo{}
	dest := func(i int) []interface{} {
		nodes = append(nodes, NodeInfo{})
		return []interface{}{
			&nodes[i].ID,
			&nodes[i].Name,
			&nodes[i].Address,
			&nodes[i].Description,
			&nodes[i].Schema,
			&nodes[i].APIExtensions,
		}
	}
	stmt := "SELECT id, name, address, description, schema, api_extensions FROM nodes ORDER BY id"
	err := query.SelectObjects(c.tx, dest, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fecth nodes")
	}
	return nodes, nil
}

// NodeAdd adds a node to the current list of LXD nodes that are part of the
// cluster. It returns the ID of the newly inserted row.
func (c *ClusterTx) NodeAdd(name string, address string) (int64, error) {
	columns := []string{"name", "address", "schema", "api_extensions"}
	values := []interface{}{name, address, cluster.SchemaVersion, len(version.APIExtensions)}
	return query.UpsertObject(c.tx, "nodes", columns, values)
}
