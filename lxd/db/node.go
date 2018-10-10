package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

// NodeInfo holds information about a single LXD instance in a cluster.
type NodeInfo struct {
	ID            int64     // Stable node identifier
	Name          string    // User-assigned name of the node
	Address       string    // Network address of the node
	Description   string    // Node description (optional)
	Schema        int       // Schema version of the LXD code running the node
	APIExtensions int       // Number of API extensions of the LXD code running on the node
	Heartbeat     time.Time // Timestamp of the last heartbeat
}

// IsOffline returns true if the last successful heartbeat time of the node is
// older than the given threshold.
func (n NodeInfo) IsOffline(threshold time.Duration) bool {
	return nodeIsOffline(threshold, n.Heartbeat)
}

// Version returns the node's version, composed by its schema level and
// number of extensions.
func (n NodeInfo) Version() [2]int {
	return [2]int{n.Schema, n.APIExtensions}
}

// NodeByAddress returns the node with the given network address.
func (c *ClusterTx) NodeByAddress(address string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(false /* not pending */, "address=?", address)
	if err != nil {
		return null, err
	}
	switch len(nodes) {
	case 0:
		return null, ErrNoSuchObject
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// NodePendingByAddress returns the pending node with the given network address.
func (c *ClusterTx) NodePendingByAddress(address string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(true /*pending */, "address=?", address)
	if err != nil {
		return null, err
	}
	switch len(nodes) {
	case 0:
		return null, ErrNoSuchObject
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// NodeByName returns the node with the given name.
func (c *ClusterTx) NodeByName(name string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(false /* not pending */, "name=?", name)
	if err != nil {
		return null, err
	}
	switch len(nodes) {
	case 0:
		return null, ErrNoSuchObject
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// NodeName returns the name of the node this method is invoked on.
func (c *ClusterTx) NodeName() (string, error) {
	stmt := "SELECT name FROM nodes WHERE id=?"
	names, err := query.SelectStrings(c.tx, stmt, c.nodeID)
	if err != nil {
		return "", err
	}
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("inconsistency: non-unique node ID")
	}
}

// NodeAddress returns the address of the node this method is invoked on.
func (c *ClusterTx) NodeAddress() (string, error) {
	stmt := "SELECT address FROM nodes WHERE id=?"
	addresses, err := query.SelectStrings(c.tx, stmt, c.nodeID)
	if err != nil {
		return "", err
	}
	switch len(addresses) {
	case 0:
		return "", nil
	case 1:
		return addresses[0], nil
	default:
		return "", fmt.Errorf("inconsistency: non-unique node ID")
	}
}

// NodeIsOutdated returns true if there's some cluster node having an API or
// schema version greater than the node this method is invoked on.
func (c *ClusterTx) NodeIsOutdated() (bool, error) {
	nodes, err := c.nodes(false /* not pending */, "")
	if err != nil {
		return false, errors.Wrap(err, "Failed to fetch nodes")
	}

	// Figure our own version.
	version := [2]int{}
	for _, node := range nodes {
		if node.ID == c.nodeID {
			version = node.Version()
		}
	}
	if version[0] == 0 || version[1] == 0 {
		return false, fmt.Errorf("Inconsistency: local node not found")
	}

	// Check if any of the other nodes is greater than us.
	for _, node := range nodes {
		if node.ID == c.nodeID {
			continue
		}
		n, err := util.CompareVersions(node.Version(), version)
		if err != nil {
			errors.Wrapf(err, "Failed to compare with version of node %s", node.Name)
		}

		if n == 1 {
			// The other node's version is greater than ours.
			return true, nil
		}
	}

	return false, nil
}

// Nodes returns all LXD nodes part of the cluster.
//
// If this LXD instance is not clustered, a list with a single node whose
// address is 0.0.0.0 is returned.
func (c *ClusterTx) Nodes() ([]NodeInfo, error) {
	return c.nodes(false /* not pending */, "")
}

// NodesCount returns the number of nodes in the LXD cluster.
//
// Since there's always at least one node row, even when not-clustered, the
// return value is greater than zero
func (c *ClusterTx) NodesCount() (int, error) {
	count, err := query.Count(c.tx, "nodes", "")
	if err != nil {
		return 0, errors.Wrap(err, "failed to count existing nodes")
	}
	return count, nil
}

// NodeRename changes the name of an existing node.
//
// Return an error if a node with the same name already exists.
func (c *ClusterTx) NodeRename(old, new string) error {
	count, err := query.Count(c.tx, "nodes", "name=?", new)
	if err != nil {
		return errors.Wrap(err, "failed to check existing nodes")
	}
	if count != 0 {
		return ErrAlreadyDefined
	}
	stmt := `UPDATE nodes SET name=? WHERE name=?`
	result, err := c.tx.Exec(stmt, new, old)
	if err != nil {
		return errors.Wrap(err, "failed to update node name")
	}
	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows count")
	}
	if n != 1 {
		return fmt.Errorf("expected to update one row, not %d", n)
	}
	return nil
}

// Nodes returns all LXD nodes part of the cluster.
func (c *ClusterTx) nodes(pending bool, where string, args ...interface{}) ([]NodeInfo, error) {
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
			&nodes[i].Heartbeat,
		}
	}
	if pending {
		args = append([]interface{}{1}, args...)
	} else {
		args = append([]interface{}{0}, args...)
	}
	sql := `
SELECT id, name, address, description, schema, api_extensions, heartbeat FROM nodes WHERE pending=? `
	if where != "" {
		sql += fmt.Sprintf("AND %s ", where)
	}
	sql += "ORDER BY id"
	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch nodes")
	}
	return nodes, nil
}

// NodeAdd adds a node to the current list of LXD nodes that are part of the
// cluster. It returns the ID of the newly inserted row.
func (c *ClusterTx) NodeAdd(name string, address string) (int64, error) {
	columns := []string{"name", "address", "schema", "api_extensions"}
	values := []interface{}{name, address, cluster.SchemaVersion, version.APIExtensionsCount()}
	return query.UpsertObject(c.tx, "nodes", columns, values)
}

// NodePending toggles the pending flag for the node. A node is pending when
// it's been accepted in the cluster, but has not yet actually joined it.
func (c *ClusterTx) NodePending(id int64, pending bool) error {
	value := 0
	if pending {
		value = 1
	}
	result, err := c.tx.Exec("UPDATE nodes SET pending=? WHERE id=?", value, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("query updated %d rows instead of 1", n)
	}
	return nil
}

// NodeUpdate updates the name an address of a node.
func (c *ClusterTx) NodeUpdate(id int64, name string, address string) error {
	result, err := c.tx.Exec("UPDATE nodes SET name=?, address=? WHERE id=?", name, address, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("query updated %d rows instead of 1", n)
	}
	return nil
}

// NodeRemove removes the node with the given id.
func (c *ClusterTx) NodeRemove(id int64) error {
	result, err := c.tx.Exec("DELETE FROM nodes WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("query deleted %d rows instead of 1", n)
	}
	return nil
}

// NodeHeartbeat updates the heartbeat column of the node with the given address.
func (c *ClusterTx) NodeHeartbeat(address string, heartbeat time.Time) error {
	stmt := "UPDATE nodes SET heartbeat=? WHERE address=?"
	result, err := c.tx.Exec(stmt, heartbeat, address)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("expected to update one row and not %d", n)
	}
	return nil
}

// NodeIsEmpty returns an empty string if the node with the given ID has no
// containers or images associated with it. Otherwise, it returns a message
// say what's left.
func (c *ClusterTx) NodeIsEmpty(id int64) (string, error) {
	// Check if the node has any containers.
	containers, err := query.SelectStrings(c.tx, "SELECT name FROM containers WHERE node_id=?", id)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get containers for node %d", id)
	}
	if len(containers) > 0 {
		message := fmt.Sprintf(
			"node still has the following containers: %s", strings.Join(containers, ", "))
		return message, nil
	}

	// Check if the node has any images available only in.
	images := []struct {
		fingerprint string
		nodeID      int64
	}{}
	dest := func(i int) []interface{} {
		images = append(images, struct {
			fingerprint string
			nodeID      int64
		}{})
		return []interface{}{&images[i].fingerprint, &images[i].nodeID}

	}
	stmt, err := c.tx.Prepare(`
SELECT fingerprint, node_id FROM images JOIN images_nodes ON images.id=images_nodes.image_id`)
	if err != nil {
		return "", err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get image list for node %d", id)
	}
	index := map[string][]int64{} // Map fingerprints to IDs of nodes
	for _, image := range images {
		index[image.fingerprint] = append(index[image.fingerprint], image.nodeID)
	}

	fingerprints := []string{}
	for fingerprint, ids := range index {
		if len(ids) > 1 {
			continue
		}
		if ids[0] == id {
			fingerprints = append(fingerprints, fingerprint)
		}
	}

	if len(fingerprints) > 0 {
		message := fmt.Sprintf(
			"node still has the following images: %s", strings.Join(fingerprints, ", "))
		return message, nil
	}

	return "", nil
}

// NodeClear removes any container or image associated with this node.
func (c *ClusterTx) NodeClear(id int64) error {
	_, err := c.tx.Exec("DELETE FROM containers WHERE node_id=?", id)
	if err != nil {
		return err
	}

	// Get the IDs of the images this node is hosting.
	ids, err := query.SelectIntegers(c.tx, "SELECT image_id FROM images_nodes WHERE node_id=?", id)
	if err != nil {
		return err
	}

	// Delete the association
	_, err = c.tx.Exec("DELETE FROM images_nodes WHERE node_id=?", id)
	if err != nil {
		return err
	}

	// Delete the image as well if this was the only node with it.
	for _, id := range ids {
		count, err := query.Count(c.tx, "images_nodes", "image_id=?", id)
		if err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		_, err = c.tx.Exec("DELETE FROM images WHERE id=?", id)
		if err != nil {
			return err
		}
	}

	return nil
}

// NodeOfflineThreshold returns the amount of time that needs to elapse after
// which a series of unsuccessful heartbeat will make the node be considered
// offline.
func (c *ClusterTx) NodeOfflineThreshold() (time.Duration, error) {
	threshold := time.Duration(DefaultOfflineThreshold) * time.Second
	values, err := query.SelectStrings(
		c.tx, "SELECT value FROM config WHERE key='cluster.offline_threshold'")
	if err != nil {
		return -1, err
	}
	if len(values) > 0 {
		seconds, err := strconv.Atoi(values[0])
		if err != nil {
			return -1, err
		}
		threshold = time.Duration(seconds) * time.Second
	}
	return threshold, nil
}

// NodeWithLeastContainers returns the name of the non-offline node with with
// the least number of containers (either already created or being created with
// an operation).
func (c *ClusterTx) NodeWithLeastContainers() (string, error) {
	threshold, err := c.NodeOfflineThreshold()
	if err != nil {
		return "", errors.Wrap(err, "failed to get offline threshold")
	}
	nodes, err := c.Nodes()
	if err != nil {
		return "", errors.Wrap(err, "failed to get current nodes")
	}

	name := ""
	containers := -1
	for _, node := range nodes {
		if node.IsOffline(threshold) {
			continue
		}

		// Fetch the number of containers already created on this node.
		created, err := query.Count(c.tx, "containers", "node_id=?", node.ID)
		if err != nil {
			return "", errors.Wrap(err, "Failed to get containers count")
		}

		// Fetch the number of containers currently being created on this node.
		pending, err := query.Count(
			c.tx, "operations", "node_id=? AND type=?", node.ID, OperationContainerCreate)
		if err != nil {
			return "", errors.Wrap(err, "Failed to get pending containers count")
		}

		count := created + pending
		if containers == -1 || count < containers {
			containers = count
			name = node.Name
		}
	}
	return name, nil
}

// NodeUpdateVersion updates the schema and API version of the node with the
// given id. This is used only in tests.
func (c *ClusterTx) NodeUpdateVersion(id int64, version [2]int) error {
	stmt := "UPDATE nodes SET schema=?, api_extensions=? WHERE id=?"

	result, err := c.tx.Exec(stmt, version[0], version[1], id)
	if err != nil {
		return errors.Wrap(err, "Failed to update nodes table")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Failed to get affected rows")
	}

	if n != 1 {
		return fmt.Errorf("Expected exactly one row to be updated")
	}

	return nil
}

func nodeIsOffline(threshold time.Duration, heartbeat time.Time) bool {
	return heartbeat.Before(time.Now().Add(-threshold))
}

// DefaultOfflineThreshold is the default value for the
// cluster.offline_threshold configuration key, expressed in seconds.
const DefaultOfflineThreshold = 20
