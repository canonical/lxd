package cluster

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/lxd/db/query"
)

// NodesClusterGroupsRow represents a single row of the nodes_cluster_groups table.
// db:model nodes_cluster_groups
type NodesClusterGroupsRow struct {
	ID      int64 `db:"id"`
	NodeID  int64 `db:"node_id"`
	GroupID int64 `db:"group_id"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (NodesClusterGroupsRow) APIName() string {
	return "Cluster group member"
}

// NodesClusterGroups contains [NodesClusterGroupsRow] with additional joins.
// db:model nodes_cluster_groups
type NodesClusterGroups struct {
	Row NodesClusterGroupsRow

	// db:join JOIN nodes ON nodes_cluster_groups.node_id = nodes.id
	NodeName string `db:"nodes.name"`
}

// GetNodesClusterGroupsByGroupID returns node cluster group associations for a given group ID.
func GetNodesClusterGroupsByGroupID(ctx context.Context, tx *sql.Tx, groupID int64) ([]NodesClusterGroups, error) {
	return query.Select[NodesClusterGroups](ctx, tx, "WHERE nodes_cluster_groups.group_id = ? ORDER BY nodes_cluster_groups.group_id", groupID)
}

// DeleteNodesClusterGroupsByGroupID deletes all node cluster group associations for the given group ID.
func DeleteNodesClusterGroupsByGroupID(ctx context.Context, tx *sql.Tx, groupID int64) error {
	_, err := query.DeleteMany[NodesClusterGroupsRow, *NodesClusterGroupsRow](ctx, tx, "WHERE group_id = ?", groupID)
	return err
}
