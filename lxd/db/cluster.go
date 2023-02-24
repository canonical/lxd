//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// ClusterGroup is a value object holding db-related details about a cluster group.
type ClusterGroup struct {
	ID          int
	Name        string
	Description string
	Nodes       []string
}

// ClusterGroupFilter specifies potential query parameter fields.
type ClusterGroupFilter struct {
	ID   *int
	Name *string
}

var clusterGroupObjects = cluster.RegisterStmt(`
SELECT cluster_groups.id, cluster_groups.name, coalesce(cluster_groups.description, '')
  FROM cluster_groups
  ORDER BY cluster_groups.name
`)

var clusterGroupObjectsByName = cluster.RegisterStmt(`
SELECT cluster_groups.id, cluster_groups.name, coalesce(cluster_groups.description, '')
  FROM cluster_groups
  WHERE cluster_groups.name = ? ORDER BY cluster_groups.name
`)

var clusterGroupCreate = cluster.RegisterStmt(`
INSERT INTO cluster_groups (name, description)
  VALUES (?, ?)
`)

var clusterGroupID = cluster.RegisterStmt(`
SELECT cluster_groups.id FROM cluster_groups
  WHERE cluster_groups.name = ?
`)

var clusterGroupRename = cluster.RegisterStmt(`
UPDATE cluster_groups SET name = ? WHERE name = ?
`)

var clusterGroupDeleteByName = cluster.RegisterStmt(`
DELETE FROM cluster_groups WHERE name = ?
`)

var clusterGroupUpdate = cluster.RegisterStmt(`
UPDATE cluster_groups
  SET name = ?, description = ?
 WHERE id = ?
`)

var clusterGroupDeleteNodesRef = cluster.RegisterStmt(`
DELETE FROM nodes_cluster_groups WHERE group_id = ?
`)

// GetClusterGroups returns all available ClusterGroups.
// generator: ClusterGroup GetMany
func (c *ClusterTx) GetClusterGroups(filter ClusterGroupFilter) ([]ClusterGroup, error) {
	// Result slice.
	objects := make([]ClusterGroup, 0)

	// Pick the prepared statement and arguments to use based on active criteria.
	var stmt *sql.Stmt
	var args []any
	var err error

	if filter.Name != nil && filter.ID == nil {
		stmt, err = cluster.Stmt(c.tx, clusterGroupObjectsByName)
		if err != nil {
			return nil, fmt.Errorf("Failed to prepare statement: %w", err)
		}

		args = []any{
			filter.Name,
		}
	} else if filter.ID == nil && filter.Name == nil {
		stmt, err = cluster.Stmt(c.tx, clusterGroupObjects)
		if err != nil {
			return nil, fmt.Errorf("Failed to prepare statement: %w", err)
		}

		args = []any{}
	} else {
		return nil, fmt.Errorf("No statement exists for the given Filter")
	}

	// Select.
	err = query.SelectObjects(context.TODO(), stmt, func(scan func(dest ...any) error) error {
		group := ClusterGroup{}
		err := scan(&group.ID, &group.Name, &group.Description)
		if err != nil {
			return err
		}

		objects = append(objects, group)

		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch cluster groups: %w", err)
	}

	// Get nodes in cluster group.
	for i := 0; i < len(objects); i++ {
		objects[i].Nodes, err = c.GetClusterGroupNodes(context.TODO(), objects[i].Name)
		if err != nil {
			return nil, err
		}
	}

	return objects, nil
}

// GetClusterGroup returns the ClusterGroup with the given key.
// generator: ClusterGroup GetOne
func (c *ClusterTx) GetClusterGroup(name string) (*ClusterGroup, error) {
	filter := ClusterGroupFilter{}
	filter.Name = &name

	objects, err := c.GetClusterGroups(filter)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch cluster group: %w", err)
	}

	switch len(objects) {
	case 0:
		return nil, api.StatusErrorf(http.StatusNotFound, "Cluster group not found")
	case 1:
		return &objects[0], nil
	default:
		return nil, fmt.Errorf("More than one cluster group matches")
	}
}

// GetClusterGroupID return the ID of the ClusterGroup with the given key.
// generator: ClusterGroup ID
func (c *ClusterTx) GetClusterGroupID(name string) (int64, error) {
	stmt, err := cluster.Stmt(c.tx, clusterGroupID)
	if err != nil {
		return -1, fmt.Errorf("Failed to prepare statement: %w", err)
	}

	rows, err := stmt.Query(name)
	if err != nil {
		return -1, fmt.Errorf("Failed to get cluster group ID: %w", err)
	}

	defer func() { _ = rows.Close() }()

	// Ensure we read one and only one row.
	if !rows.Next() {
		return -1, api.StatusErrorf(http.StatusNotFound, "Cluster group not found")
	}

	var id int64
	err = rows.Scan(&id)
	if err != nil {
		return -1, fmt.Errorf("Failed to scan ID: %w", err)
	}

	if rows.Next() {
		return -1, fmt.Errorf("More than one row returned")
	}

	err = rows.Err()
	if err != nil {
		return -1, fmt.Errorf("Result set failure: %w", err)
	}

	return id, nil
}

// ClusterGroupExists checks if a ClusterGroup with the given key exists.
// generator: ClusterGroup Exists
func (c *ClusterTx) ClusterGroupExists(name string) (bool, error) {
	_, err := c.GetClusterGroupID(name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// CreateClusterGroup adds a new ClusterGroup to the database.
// generator: ClusterGroup Create
func (c *ClusterTx) CreateClusterGroup(object ClusterGroup) (int64, error) {
	// Check if a ClusterGroup with the same key exists.
	exists, err := c.ClusterGroupExists(object.Name)
	if err != nil {
		return -1, fmt.Errorf("Failed to check for duplicates: %w", err)
	}

	if exists {
		return -1, fmt.Errorf("This cluster group already exists")
	}

	args := make([]any, 2)

	// Populate the statement arguments.
	args[0] = object.Name
	args[1] = object.Description

	// Prepared statement to use.
	stmt, err := cluster.Stmt(c.tx, clusterGroupCreate)
	if err != nil {
		return -1, fmt.Errorf("Failed to prepare statement: %w", err)
	}

	// Execute the statement.
	result, err := stmt.Exec(args...)
	if err != nil {
		return -1, fmt.Errorf("Failed to create cluster group: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to fetch cluster group ID: %w", err)
	}

	// Insert nodes reference.
	err = addNodesToClusterGroup(c.tx, int(id), object.Nodes)
	if err != nil {
		return -1, fmt.Errorf("Insert nodes for cluster group: %w", err)
	}

	return id, nil
}

// RenameClusterGroup renames the ClusterGroup matching the given key parameters.
// generator: ClusterGroup Rename
func (c *ClusterTx) RenameClusterGroup(name string, to string) error {
	stmt, err := cluster.Stmt(c.tx, clusterGroupRename)
	if err != nil {
		return fmt.Errorf("Failed to prepare statement: %w", err)
	}

	result, err := stmt.Exec(to, name)
	if err != nil {
		return fmt.Errorf("Failed to rename cluster group: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query affected %d rows instead of 1", n)
	}

	return nil
}

// DeleteClusterGroup deletes the ClusterGroup matching the given key parameters.
// generator: ClusterGroup DeleteOne-by-Name
func (c *ClusterTx) DeleteClusterGroup(name string) error {
	stmt, err := cluster.Stmt(c.tx, clusterGroupDeleteByName)
	if err != nil {
		return fmt.Errorf("Failed to prepare statement: %w", err)
	}

	result, err := stmt.Exec(name)
	if err != nil {
		return fmt.Errorf("Failed to delete cluster group: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query deleted %d rows instead of 1", n)
	}

	return nil
}

// UpdateClusterGroup updates the ClusterGroup matching the given key parameters.
// generator: ClusterGroup Update
func (c *ClusterTx) UpdateClusterGroup(name string, object ClusterGroup) error {
	id, err := c.GetClusterGroupID(name)
	if err != nil {
		return fmt.Errorf("Failed to get cluster group: %w", err)
	}

	stmt, err := cluster.Stmt(c.tx, clusterGroupUpdate)
	if err != nil {
		return fmt.Errorf("Failed to prepare statement: %w", err)
	}

	result, err := stmt.Exec(object.Name, object.Description, id)
	if err != nil {
		return fmt.Errorf("Failed to update cluster group: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	// Delete current nodes.
	stmt, err = cluster.Stmt(c.tx, clusterGroupDeleteNodesRef)
	if err != nil {
		return fmt.Errorf("Failed to prepare statement: %w", err)
	}

	_, err = stmt.Exec(id)
	if err != nil {
		return fmt.Errorf("Failed to delete current nodes: %w", err)
	}

	// Insert nodes reference.
	err = addNodesToClusterGroup(c.tx, int(id), object.Nodes)
	if err != nil {
		return fmt.Errorf("Failed to insert nodes for cluster group: %w", err)
	}

	return nil
}

// ClusterGroupToAPI is a convenience to convert a ClusterGroup db struct into
// an API cluster group struct.
func ClusterGroupToAPI(clusterGroup *ClusterGroup, nodes []string) *api.ClusterGroup {
	c := &api.ClusterGroup{
		ClusterGroupPut: api.ClusterGroupPut{
			Description: clusterGroup.Description,
			Members:     nodes,
		},
		ClusterGroupPost: api.ClusterGroupPost{
			Name: clusterGroup.Name,
		},
	}

	return c
}

// GetClusterGroupNodes returns a list of nodes of the given cluster group.
func (c *ClusterTx) GetClusterGroupNodes(ctx context.Context, groupName string) ([]string, error) {
	q := `SELECT nodes.name FROM nodes_cluster_groups
JOIN nodes ON nodes.id = nodes_cluster_groups.node_id
JOIN cluster_groups ON cluster_groups.id = nodes_cluster_groups.group_id
WHERE cluster_groups.name = ?`

	return query.SelectStrings(ctx, c.tx, q, groupName)
}

// GetClusterGroupURIs returns all available ClusterGroup URIs.
// generator: ClusterGroup URIs
func (c *ClusterTx) GetClusterGroupURIs(ctx context.Context, filter ClusterGroupFilter) ([]string, error) {
	var args []any
	var sql string
	if filter.Name != nil && filter.ID == nil {
		sql = `SELECT cluster_groups.name FROM cluster_groups
WHERE cluster_groups.name = ? ORDER BY cluster_groups.name
`
		args = []any{
			filter.Name,
		}
	} else if filter.ID == nil && filter.Name == nil {
		sql = `SELECT cluster_groups.name FROM cluster_groups ORDER BY cluster_groups.name`
		args = []any{}
	} else {
		return nil, fmt.Errorf("No statement exists for the given Filter")
	}

	names, err := query.SelectStrings(ctx, c.tx, sql, args...)
	if err != nil {
		return nil, err
	}

	uris := make([]string, len(names))
	for i, name := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "cluster", "groups", name).String()
	}

	return uris, nil
}

// AddNodeToClusterGroup adds a given node to the given cluster group.
func (c *ClusterTx) AddNodeToClusterGroup(ctx context.Context, groupName string, nodeName string) error {
	groupID, err := c.GetClusterGroupID(groupName)
	if err != nil {
		return fmt.Errorf("Failed to get cluster group ID: %w", err)
	}

	nodeInfo, err := c.GetNodeByName(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("Failed to get node info: %w", err)
	}

	_, err = c.tx.Exec(`INSERT INTO nodes_cluster_groups (node_id, group_id) VALUES(?, ?)`, nodeInfo.ID, groupID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveNodeFromClusterGroup removes a given node from the given group name.
func (c *ClusterTx) RemoveNodeFromClusterGroup(ctx context.Context, groupName string, nodeName string) error {
	groupID, err := c.GetClusterGroupID(groupName)
	if err != nil {
		return fmt.Errorf("Failed to get cluster group ID: %w", err)
	}

	nodeInfo, err := c.GetNodeByName(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("Failed to get node info: %w", err)
	}

	_, err = c.tx.Exec(`DELETE FROM nodes_cluster_groups WHERE node_id = ? AND group_id = ?`, nodeInfo.ID, groupID)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterGroupsWithNode returns a list of cluster group names the given node belongs to.
func (c *ClusterTx) GetClusterGroupsWithNode(ctx context.Context, nodeName string) ([]string, error) {
	q := `SELECT cluster_groups.name FROM nodes_cluster_groups
JOIN cluster_groups ON cluster_groups.id = nodes_cluster_groups.group_id
JOIN nodes ON nodes.id = nodes_cluster_groups.node_id
WHERE nodes.name = ?`

	return query.SelectStrings(ctx, c.tx, q, nodeName)
}

// ToAPI returns a LXD API entry.
func (c *ClusterGroup) ToAPI() (*api.ClusterGroup, error) {
	result := api.ClusterGroup{
		ClusterGroupPut: api.ClusterGroupPut{
			Description: c.Description,
			Members:     c.Nodes,
		},
		ClusterGroupPost: api.ClusterGroupPost{
			Name: c.Name,
		},
	}

	return &result, nil
}

// addNodesToClusterGroup adds the given nodes the the cluster group with the given ID.
func addNodesToClusterGroup(tx *sql.Tx, id int, nodes []string) error {
	str := `
INSERT INTO nodes_cluster_groups (group_id, node_id)
  VALUES (
    ?,
    (SELECT nodes.id
     FROM nodes
     WHERE nodes.name = ?)
  )`
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for _, node := range nodes {
		_, err = stmt.Exec(id, node)
		if err != nil {
			logger.Debugf("Error adding node %q to cluster group: %s", node, err)
			return err
		}
	}

	return nil
}
