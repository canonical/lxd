//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
)

// ClusterRole represents the role of a member in a cluster.
type ClusterRole string

// ClusterRoleDatabase represents the database role in a cluster.
const ClusterRoleDatabase = ClusterRole("database")

// ClusterRoleDatabaseStandBy represents the database stand-by role in a cluster.
const ClusterRoleDatabaseStandBy = ClusterRole("database-standby")

// ClusterRoleDatabaseLeader represents the database leader role in a cluster.
const ClusterRoleDatabaseLeader = ClusterRole("database-leader")

// ClusterRoleEventHub represents a cluster member who operates as an event hub.
const ClusterRoleEventHub = ClusterRole("event-hub")

// ClusterRoleOVNChassis represents a cluster member who operates as an OVN chassis.
const ClusterRoleOVNChassis = ClusterRole("ovn-chassis")

// ClusterRoles maps role ids into human-readable names.
//
// Note: the database role is currently stored directly in the raft
// configuration which acts as single source of truth for it. This map should
// only contain LXD-specific cluster roles.
var ClusterRoles = map[int]ClusterRole{
	1: ClusterRoleEventHub,
	2: ClusterRoleOVNChassis,
}

// Numeric type codes identifying different cluster member states.
const (
	ClusterMemberStateCreated   = 0
	ClusterMemberStatePending   = 1
	ClusterMemberStateEvacuated = 2
)

// NodeInfo holds information about a single LXD instance in a cluster.
type NodeInfo struct {
	ID            int64             // Stable node identifier
	Name          string            // User-assigned name of the node
	Address       string            // Network address of the node
	Description   string            // Node description (optional)
	Schema        int               // Schema version of the LXD code running the node
	APIExtensions int               // Number of API extensions of the LXD code running on the node
	Heartbeat     time.Time         // Timestamp of the last heartbeat
	Roles         []ClusterRole     // List of cluster roles
	Architecture  int               // Node architecture
	State         int               // Node state
	Config        map[string]string // Configuration for the node
	Groups        []string          // Cluster groups
}

// IsOffline returns true if the last successful heartbeat time of the node is
// older than the given threshold.
func (n NodeInfo) IsOffline(threshold time.Duration) bool {
	return nodeIsOffline(threshold, n.Heartbeat)
}

// ToAPI returns a LXD API entry.
func (n NodeInfo) ToAPI(cluster *Cluster, node *Node, leader string) (*api.ClusterMember, error) {
	// Load some needed data.
	var err error
	var offlineThreshold time.Duration
	var maxVersion [2]int
	var failureDomain string

	// From cluster database.
	err = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		// Get offline threshold.
		offlineThreshold, err = tx.GetNodeOfflineThreshold()
		if err != nil {
			return fmt.Errorf("Load offline threshold config: %w", err)
		}

		// Get failure domains.
		nodesDomains, err := tx.GetNodesFailureDomains()
		if err != nil {
			return fmt.Errorf("Load nodes failure domains: %w", err)
		}

		domainsNames, err := tx.GetFailureDomainsNames()
		if err != nil {
			return fmt.Errorf("Load failure domains names: %w", err)
		}

		domainID := nodesDomains[n.Address]
		failureDomain = domainsNames[domainID]

		// Get the highest schema and API versions.
		maxVersion, err = tx.GetNodeMaxVersion()
		if err != nil {
			return fmt.Errorf("Get max version: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// From local database.
	var raftNode *RaftNode
	err = node.Transaction(func(tx *NodeTx) error {
		nodes, err := tx.GetRaftNodes()
		if err != nil {
			return fmt.Errorf("Load offline threshold config: %w", err)
		}

		for _, node := range nodes {
			if node.Address != n.Address {
				continue
			}

			raftNode = &node
			break
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	result := api.ClusterMember{}
	result.Description = n.Description
	result.ServerName = n.Name
	result.URL = fmt.Sprintf("https://%s", n.Address)
	result.Database = false
	result.Config = n.Config

	result.Roles = make([]string, 0, len(n.Roles))
	for _, r := range n.Roles {
		result.Roles = append(result.Roles, string(r))
	}

	result.Groups = n.Groups

	// Check if node is the leader node
	if leader == n.Address {
		result.Roles = append(result.Roles, string(ClusterRoleDatabaseLeader))
		result.Database = true
	}

	if raftNode != nil && raftNode.Role == RaftVoter {
		result.Roles = append(result.Roles, string(ClusterRoleDatabase))
		result.Database = true
	}

	if raftNode != nil && raftNode.Role == RaftStandBy {
		result.Roles = append(result.Roles, string(ClusterRoleDatabaseStandBy))
		result.Database = true
	}

	result.Architecture, err = osarch.ArchitectureName(n.Architecture)
	if err != nil {
		return nil, err
	}

	result.FailureDomain = failureDomain

	// Set state and message.
	result.Status = "Online"
	result.Message = "Fully operational"

	if n.State == ClusterMemberStateEvacuated {
		result.Status = "Evacuated"
		result.Message = "Unavailable due to maintenance"
	} else if n.IsOffline(offlineThreshold) {
		result.Status = "Offline"
	} else {
		// Check if up to date.
		n, err := util.CompareVersions(maxVersion, n.Version())
		if err != nil {
			return nil, err
		}

		if n == 1 {
			result.Status = "Blocked"
			result.Message = "Needs updating to newer version"
		}
	}

	if n.IsOffline(offlineThreshold) {
		result.Message = fmt.Sprintf("No heartbeat for %s (%s)", time.Since(n.Heartbeat), n.Heartbeat)
	}

	return &result, nil
}

// Version returns the node's version, composed by its schema level and
// number of extensions.
func (n NodeInfo) Version() [2]int {
	return [2]int{n.Schema, n.APIExtensions}
}

// GetNodeByAddress returns the node with the given network address.
func (c *ClusterTx) GetNodeByAddress(address string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(false /* not pending */, "address=?", address)
	if err != nil {
		return null, err
	}

	switch len(nodes) {
	case 0:
		return null, api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// GetNodeMaxVersion returns the highest version possible on the cluster.
func (c *ClusterTx) GetNodeMaxVersion() ([2]int, error) {
	version := [2]int{}

	// Get the maximum DB schema.
	var maxSchema int
	row := c.tx.QueryRow("SELECT MAX(schema) FROM nodes")
	err := row.Scan(&maxSchema)
	if err != nil {
		return version, err
	}

	// Get the maximum API extension.
	var maxAPI int
	row = c.tx.QueryRow("SELECT MAX(api_extensions) FROM nodes")
	err = row.Scan(&maxAPI)
	if err != nil {
		return version, err
	}

	// Compute the combined version.
	version = [2]int{maxSchema, maxAPI}

	return version, nil
}

// GetNodeWithID returns the node with the given ID.
func (c *ClusterTx) GetNodeWithID(nodeID int) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(false /* not pending */, "id=?", nodeID)
	if err != nil {
		return null, err
	}

	switch len(nodes) {
	case 0:
		return null, api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("More than one cluster member matches")
	}
}

// GetPendingNodeByAddress returns the pending node with the given network address.
func (c *ClusterTx) GetPendingNodeByAddress(address string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(true /*pending */, "address=?", address)
	if err != nil {
		return null, err
	}

	switch len(nodes) {
	case 0:
		return null, api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("More than one cluster member matches")
	}
}

// GetNodeByName returns the node with the given name.
func (c *ClusterTx) GetNodeByName(name string) (NodeInfo, error) {
	null := NodeInfo{}
	nodes, err := c.nodes(false /* not pending */, "name=?", name)
	if err != nil {
		return null, err
	}

	switch len(nodes) {
	case 0:
		return null, api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
	case 1:
		return nodes[0], nil
	default:
		return null, fmt.Errorf("More than one cluster member matches")
	}
}

// GetLocalNodeName returns the name of the node this method is invoked on.
// Usually you should not use this function directly but instead use the cached State.ServerName value.
func (c *ClusterTx) GetLocalNodeName() (string, error) {
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

// GetLocalNodeAddress returns the address of the node this method is invoked on.
func (c *ClusterTx) GetLocalNodeAddress() (string, error) {
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
		return false, fmt.Errorf("Failed to fetch nodes: %w", err)
	}

	// Figure our own version.
	version := [2]int{}
	for _, node := range nodes {
		if node.ID == c.nodeID {
			version = node.Version()
		}
	}
	if version[0] == 0 || version[1] == 0 {
		return false, fmt.Errorf("Inconsistency: local member not found")
	}

	// Check if any of the other nodes is greater than us.
	for _, node := range nodes {
		if node.ID == c.nodeID {
			continue
		}

		n, err := util.CompareVersions(node.Version(), version)
		if err != nil {
			return false, fmt.Errorf("Failed to compare with version of member %s: %w", node.Name, err)
		}

		if n == 1 {
			// The other node's version is greater than ours.
			return true, nil
		}
	}

	return false, nil
}

// GetNodes returns all LXD nodes part of the cluster.
//
// If this LXD instance is not clustered, a list with a single node whose
// address is 0.0.0.0 is returned.
func (c *ClusterTx) GetNodes() ([]NodeInfo, error) {
	return c.nodes(false /* not pending */, "")
}

// GetNodesCount returns the number of nodes in the LXD cluster.
//
// Since there's always at least one node row, even when not-clustered, the
// return value is greater than zero.
func (c *ClusterTx) GetNodesCount() (int, error) {
	count, err := query.Count(c.tx, "nodes", "")
	if err != nil {
		return 0, fmt.Errorf("failed to count existing nodes: %w", err)
	}

	return count, nil
}

// RenameNode changes the name of an existing node.
//
// Return an error if a node with the same name already exists.
func (c *ClusterTx) RenameNode(old, new string) error {
	count, err := query.Count(c.tx, "nodes", "name=?", new)
	if err != nil {
		return fmt.Errorf("failed to check existing nodes: %w", err)
	}

	if count != 0 {
		return ErrAlreadyDefined
	}

	stmt := `UPDATE nodes SET name=? WHERE name=?`
	result, err := c.tx.Exec(stmt, new, old)
	if err != nil {
		return fmt.Errorf("failed to update node name: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows count: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("expected to update one row, not %d", n)
	}

	return nil
}

// SetDescription changes the description of the given node.
func (c *ClusterTx) SetDescription(id int64, description string) error {
	stmt := `UPDATE nodes SET description=? WHERE id=?`
	result, err := c.tx.Exec(stmt, description, id)
	if err != nil {
		return fmt.Errorf("Failed to update node name: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get rows count: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Expected to update one row, not %d", n)
	}

	return nil
}

// Nodes returns all LXD nodes part of the cluster.
func (c *ClusterTx) nodes(pending bool, where string, args ...any) ([]NodeInfo, error) {
	// Get node roles
	sql := "SELECT node_id, role FROM nodes_roles"

	nodeRoles := map[int64][]ClusterRole{}
	err := c.QueryScan(sql, func(scan func(dest ...any) error) error {
		var nodeID int64
		var role int

		err := scan(&nodeID, &role)
		if err != nil {
			return err
		}

		if nodeRoles[nodeID] == nil {
			nodeRoles[nodeID] = []ClusterRole{}
		}

		roleName := string(ClusterRoles[role])
		nodeRoles[nodeID] = append(nodeRoles[nodeID], ClusterRole(roleName))

		return nil
	})
	if err != nil && err.Error() != "no such table: nodes_roles" {
		// Don't fail on a missing table, we need to handle updates
		return nil, err
	}

	// Get node groups
	sql = `SELECT node_id, cluster_groups.name FROM nodes_cluster_groups
JOIN cluster_groups ON cluster_groups.id = nodes_cluster_groups.group_id`
	nodeGroups := map[int64][]string{}

	err = c.QueryScan(sql, func(scan func(dest ...any) error) error {
		var nodeID int64
		var group string

		err := scan(&nodeID, &group)
		if err != nil {
			return err
		}

		if nodeGroups[nodeID] == nil {
			nodeGroups[nodeID] = []string{}
		}

		nodeGroups[nodeID] = append(nodeGroups[nodeID], group)

		return nil
	})
	if err != nil && err.Error() != "no such table: nodes_cluster_groups" {
		// Don't fail on a missing table, we need to handle updates
		return nil, err
	}

	// Process node entries
	nodes := []NodeInfo{}
	dest := func(i int) []any {
		nodes = append(nodes, NodeInfo{})
		return []any{
			&nodes[i].ID,
			&nodes[i].Name,
			&nodes[i].Address,
			&nodes[i].Description,
			&nodes[i].Schema,
			&nodes[i].APIExtensions,
			&nodes[i].Heartbeat,
			&nodes[i].Architecture,
			&nodes[i].State,
		}
	}

	// Get the node entries
	sql = "SELECT id, name, address, description, schema, api_extensions, heartbeat, arch, state FROM nodes "

	if pending {
		// Include only pending nodes
		sql += "WHERE state=? "
	} else {
		// Include created and evacuated nodes
		sql += "WHERE state!=? "
	}

	args = append([]any{ClusterMemberStatePending}, args...)

	if where != "" {
		sql += fmt.Sprintf("AND %s ", where)
	}

	sql += "ORDER BY id"

	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}

	defer func() { _ = stmt.Close() }()

	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch nodes: %w", err)
	}

	// Add the roles
	for i, node := range nodes {
		roles, ok := nodeRoles[node.ID]
		if ok {
			nodes[i].Roles = roles
		}
	}

	// Add the groups
	for i, node := range nodes {
		groups, ok := nodeGroups[node.ID]
		if ok {
			nodes[i].Groups = groups
		}
	}

	config, err := cluster.GetConfig(context.TODO(), c.Tx(), "node", cluster.ConfigFilter{})
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch nodes config: %w", err)
	}

	for i := range nodes {
		data, ok := config[int(nodes[i].ID)]
		if !ok {
			nodes[i].Config = map[string]string{}
		} else {
			nodes[i].Config = data
		}
	}

	return nodes, nil
}

// CreateNode adds a node to the current list of LXD nodes that are part of the
// cluster. The node's architecture will be the architecture of the machine the
// method is being run on. It returns the ID of the newly inserted row.
func (c *ClusterTx) CreateNode(name string, address string) (int64, error) {
	arch, err := osarch.ArchitectureGetLocalID()
	if err != nil {
		return -1, err
	}

	return c.CreateNodeWithArch(name, address, arch)
}

// CreateNodeWithArch is the same as NodeAdd, but lets setting the node
// architecture explicitly.
func (c *ClusterTx) CreateNodeWithArch(name string, address string, arch int) (int64, error) {
	columns := []string{"name", "address", "schema", "api_extensions", "arch", "description"}
	values := []any{name, address, cluster.SchemaVersion, version.APIExtensionsCount(), arch, ""}
	return query.UpsertObject(c.tx, "nodes", columns, values)
}

// SetNodePendingFlag toggles the pending flag for the node. A node is pending when
// it's been accepted in the cluster, but has not yet actually joined it.
func (c *ClusterTx) SetNodePendingFlag(id int64, pending bool) error {
	value := 0
	if pending {
		value = 1
	}

	result, err := c.tx.Exec("UPDATE nodes SET state=? WHERE id=?", value, id)
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

// BootstrapNode sets the name and address of the first cluster member, with id: 1.
func (c *ClusterTx) BootstrapNode(name string, address string) error {
	result, err := c.tx.Exec("UPDATE nodes SET name=?, address=? WHERE id=1", name, address)
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

// UpdateNodeConfig updates the replaces the node's config with the specified config.
func (c *ClusterTx) UpdateNodeConfig(ctx context.Context, id int64, config map[string]string) error {
	err := cluster.UpdateConfig(ctx, c.Tx(), "node", int(id), config)
	if err != nil {
		return fmt.Errorf("Unable to update node config: %w", err)
	}

	return nil
}

// UpdateNodeRoles changes the list of roles on a member.
func (c *ClusterTx) UpdateNodeRoles(id int64, roles []ClusterRole) error {
	getRoleID := func(role ClusterRole) (int, error) {
		for k, v := range ClusterRoles {
			if v == role {
				return k, nil
			}
		}

		return -1, fmt.Errorf("Invalid cluster role %q", role)
	}

	// Translate role names to ids
	roleIDs := []int{}
	for _, role := range roles {
		// Skip internal-only roles.
		if role == ClusterRoleDatabase || role == ClusterRoleDatabaseStandBy || role == ClusterRoleDatabaseLeader {
			continue
		}

		roleID, err := getRoleID(role)
		if err != nil {
			return err
		}

		roleIDs = append(roleIDs, roleID)
	}

	// Update the database record
	_, err := c.tx.Exec("DELETE FROM nodes_roles WHERE node_id=?", id)
	if err != nil {
		return err
	}

	for _, roleID := range roleIDs {
		_, err := c.tx.Exec("INSERT INTO nodes_roles (node_id, role) VALUES (?, ?)", id, roleID)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateNodeClusterGroups changes the list of cluster groups the member belongs to.
func (c *ClusterTx) UpdateNodeClusterGroups(id int64, groups []string) error {
	nodeInfo, err := c.GetNodeWithID(int(id))
	if err != nil {
		return err
	}

	oldGroups, err := c.GetClusterGroupsWithNode(nodeInfo.Name)
	if err != nil {
		return err
	}

	skipGroups := []string{}

	// Check if node already belongs to the given groups.
	for _, newGroup := range groups {
		if shared.StringInSlice(newGroup, oldGroups) {
			// Node already belongs to this group.
			skipGroups = append(skipGroups, newGroup)
			continue
		}

		// Add node to new group.
		err = c.AddNodeToClusterGroup(newGroup, nodeInfo.Name)
		if err != nil {
			return fmt.Errorf("Failed to add member to cluster group: %w", err)
		}
	}

	for _, oldGroup := range oldGroups {
		if shared.StringInSlice(oldGroup, skipGroups) {
			continue
		}

		// Remove node from group.
		err = c.RemoveNodeFromClusterGroup(oldGroup, nodeInfo.Name)
		if err != nil {
			return fmt.Errorf("Failed to remove member from cluster group: %w", err)
		}
	}

	return nil
}

// UpdateNodeFailureDomain changes the failure domain of a node.
func (c *ClusterTx) UpdateNodeFailureDomain(id int64, domain string) error {
	var domainID any

	if domain == "" {
		return fmt.Errorf("Failure domain name can't be empty")
	}

	if domain == "default" {
		domainID = nil
	} else {
		row := c.tx.QueryRow("SELECT id FROM nodes_failure_domains WHERE name=?", domain)
		err := row.Scan(&domainID)
		if err != nil {
			if err != sql.ErrNoRows {
				return fmt.Errorf("Load failure domain name: %w", err)
			}

			result, err := c.tx.Exec("INSERT INTO nodes_failure_domains (name) VALUES (?)", domain)
			if err != nil {
				return fmt.Errorf("Create new failure domain: %w", err)
			}

			domainID, err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("Get last inserted ID: %w", err)
			}
		}
	}

	result, err := c.tx.Exec("UPDATE nodes SET failure_domain_id=? WHERE id=?", domainID, id)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}

// UpdateNodeStatus changes the state of a node.
func (c *ClusterTx) UpdateNodeStatus(id int64, state int) error {
	result, err := c.tx.Exec("UPDATE nodes SET state=? WHERE id=?", state, id)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}

// GetNodeFailureDomain returns the failure domain associated with the node with the given ID.
func (c *ClusterTx) GetNodeFailureDomain(id int64) (string, error) {
	stmt := `
SELECT coalesce(nodes_failure_domains.name,'default')
  FROM nodes LEFT JOIN nodes_failure_domains ON nodes.failure_domain_id = nodes_failure_domains.id
 WHERE nodes.id=?
`
	var domain string

	err := c.tx.QueryRow(stmt, id).Scan(&domain)
	if err != nil {
		return "", err
	}

	return domain, nil
}

// GetNodesFailureDomains returns a map associating each node address with its
// failure domain code.
func (c *ClusterTx) GetNodesFailureDomains() (map[string]uint64, error) {
	stmt, err := c.tx.Prepare("SELECT address, coalesce(failure_domain_id, 0) FROM nodes")
	if err != nil {
		return nil, err
	}

	rows := []struct {
		Address         string
		FailureDomainID int64
	}{}

	dest := func(i int) []any {
		rows = append(rows, struct {
			Address         string
			FailureDomainID int64
		}{})
		return []any{&rows[len(rows)-1].Address, &rows[len(rows)-1].FailureDomainID}
	}

	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return nil, err
	}

	domains := map[string]uint64{}

	for _, row := range rows {
		domains[row.Address] = uint64(row.FailureDomainID)
	}

	return domains, nil
}

// GetFailureDomainsNames return a map associating failure domain IDs to their
// names.
func (c *ClusterTx) GetFailureDomainsNames() (map[uint64]string, error) {
	stmt, err := c.tx.Prepare("SELECT id, name FROM nodes_failure_domains")
	if err != nil {
		return nil, err
	}

	rows := []struct {
		ID   int64
		Name string
	}{}

	dest := func(i int) []any {
		rows = append(rows, struct {
			ID   int64
			Name string
		}{})
		return []any{&rows[len(rows)-1].ID, &rows[len(rows)-1].Name}
	}

	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return nil, err
	}

	domains := map[uint64]string{
		0: "default", // Default failure domain, when not set
	}

	for _, row := range rows {
		domains[uint64(row.ID)] = row.Name
	}

	return domains, nil
}

// RemoveNode removes the node with the given id.
func (c *ClusterTx) RemoveNode(id int64) error {
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

// SetNodeHeartbeat updates the heartbeat column of the node with the given address.
func (c *ClusterTx) SetNodeHeartbeat(address string, heartbeat time.Time) error {
	stmt := "UPDATE nodes SET heartbeat=? WHERE address=?"
	result, err := c.tx.Exec(stmt, heartbeat, address)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n < 1 {
		return api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
	} else if n > 1 {
		return fmt.Errorf("Expected to update one row and not %d", n)
	}

	return nil
}

// NodeIsEmpty returns an empty string if the node with the given ID has no
// containers or images associated with it. Otherwise, it returns a message
// say what's left.
func (c *ClusterTx) NodeIsEmpty(id int64) (string, error) {
	// Check if the node has any instances.
	containers, err := query.SelectStrings(c.tx, "SELECT name FROM instances WHERE node_id=?", id)
	if err != nil {
		return "", fmt.Errorf("Failed to get instances for node %d: %w", id, err)
	}

	if len(containers) > 0 {
		message := fmt.Sprintf(
			"Node still has the following containers: %s", strings.Join(containers, ", "))
		return message, nil
	}

	// Check if the node has any images available only in it.
	images := []struct {
		fingerprint string
		nodeID      int64
	}{}
	dest := func(i int) []any {
		images = append(images, struct {
			fingerprint string
			nodeID      int64
		}{})
		return []any{&images[i].fingerprint, &images[i].nodeID}
	}

	stmt, err := c.tx.Prepare(`
SELECT fingerprint, node_id FROM images JOIN images_nodes ON images.id=images_nodes.image_id`)
	if err != nil {
		return "", err
	}

	defer func() { _ = stmt.Close() }()
	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return "", fmt.Errorf("Failed to get image list for node %d: %w", id, err)
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
			"Node still has the following images: %s", strings.Join(fingerprints, ", "))
		return message, nil
	}

	// Check if the node has any custom volumes.
	volumes, err := query.SelectStrings(
		c.tx, "SELECT storage_volumes.name FROM storage_volumes JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id WHERE storage_volumes.node_id=? AND storage_volumes.type=? AND storage_pools.driver NOT IN ('ceph', 'cephfs')",
		id, StoragePoolVolumeTypeCustom)
	if err != nil {
		return "", fmt.Errorf("Failed to get custom volumes for node %d: %w", id, err)
	}

	if len(volumes) > 0 {
		message := fmt.Sprintf(
			"Node still has the following custom volumes: %s", strings.Join(volumes, ", "))
		return message, nil
	}

	return "", nil
}

// ClearNode removes any instance or image associated with this node.
func (c *ClusterTx) ClearNode(id int64) error {
	_, err := c.tx.Exec("DELETE FROM instances WHERE node_id=?", id)
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

// GetNodeOfflineThreshold returns the amount of time that needs to elapse after
// which a series of unsuccessful heartbeat will make the node be considered
// offline.
func (c *ClusterTx) GetNodeOfflineThreshold() (time.Duration, error) {
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

// GetNodeWithLeastInstances returns the name of the non-offline node with with
// the least number of containers (either already created or being created with
// an operation). If archs is not empty, then return only nodes with an
// architecture in that list.
func (c *ClusterTx) GetNodeWithLeastInstances(archs []int, defaultArch int, group string, allowedGroups []string) (string, error) {
	threshold, err := c.GetNodeOfflineThreshold()
	if err != nil {
		return "", fmt.Errorf("Failed to get offline threshold: %w", err)
	}

	nodes, err := c.GetNodes()
	if err != nil {
		return "", fmt.Errorf("Failed to get current cluster members: %w", err)
	}

	name := ""
	containers := -1
	isDefaultArchChosen := false
	for _, node := range nodes {
		// Skip evacuated members.
		if node.State == ClusterMemberStateEvacuated || node.IsOffline(threshold) {
			continue
		}

		// Skip manually targeted members.
		if node.Config["scheduler.instance"] == "manual" {
			continue
		}

		// Skip group-only members if targeted group doesn't match.
		if node.Config["scheduler.instance"] == "group" && !shared.StringInSlice(group, node.Groups) {
			continue
		}

		// Skip if a group is requested and member isn't part of it.
		if group != "" && !shared.StringInSlice(group, node.Groups) {
			continue
		}

		// Skip if working with a restricted set of groups and member isn't part of any.
		if allowedGroups != nil {
			found := false
			for _, allowedGroup := range allowedGroups {
				if shared.StringInSlice(allowedGroup, node.Groups) {
					found = true
					break
				}
			}

			if !found {
				continue
			}
		}

		// Get member personalities too.
		personalities, err := osarch.ArchitecturePersonalities(node.Architecture)
		if err != nil {
			return "", err
		}

		supported := []int{node.Architecture}
		supported = append(supported, personalities...)

		match := false
		isDefaultArch := false
		for _, entry := range supported {
			if shared.IntInSlice(entry, archs) {
				match = true
			}

			if entry == defaultArch {
				isDefaultArch = true
			}
		}
		if len(archs) > 0 && !match {
			continue
		}

		if !isDefaultArch && isDefaultArchChosen {
			continue
		}

		// Fetch the number of instances already created on this node.
		created, err := query.Count(c.tx, "instances", "node_id=?", node.ID)
		if err != nil {
			return "", fmt.Errorf("Failed to get instances count: %w", err)
		}

		// Fetch the number of instances currently being created on this node.
		pending, err := query.Count(
			c.tx, "operations", "node_id=? AND type=?", node.ID, operationtype.InstanceCreate)
		if err != nil {
			return "", fmt.Errorf("Failed to get pending instances count: %w", err)
		}

		count := created + pending
		if containers == -1 || count < containers || (isDefaultArch && !isDefaultArchChosen) {
			containers = count
			name = node.Name
			if isDefaultArch {
				isDefaultArchChosen = true
			}
		}
	}

	return name, nil
}

// SetNodeVersion updates the schema and API version of the node with the
// given id. This is used only in tests.
func (c *ClusterTx) SetNodeVersion(id int64, version [2]int) error {
	stmt := "UPDATE nodes SET schema=?, api_extensions=? WHERE id=?"

	result, err := c.tx.Exec(stmt, version[0], version[1], id)
	if err != nil {
		return fmt.Errorf("Failed to update nodes table: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Expected exactly one row to be updated")
	}

	return nil
}

func nodeIsOffline(threshold time.Duration, heartbeat time.Time) bool {
	offlineTime := time.Now().UTC().Add(-threshold)

	return heartbeat.Before(offlineTime) || heartbeat.Equal(offlineTime)
}

// LocalNodeIsEvacuated returns whether the local member is in the evacuated state.
func (c *Cluster) LocalNodeIsEvacuated() bool {
	isEvacuated := false

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		name, err := tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		node, err := tx.GetNodeByName(name)
		if err != nil {
			return nil
		}

		isEvacuated = node.State == ClusterMemberStateEvacuated
		return nil
	})
	if err != nil {
		return false
	}

	return isEvacuated
}

// DefaultOfflineThreshold is the default value for the
// cluster.offline_threshold configuration key, expressed in seconds.
const DefaultOfflineThreshold = 20
