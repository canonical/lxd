package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
)

// entityTypeClusterMember implements entityTypeDBInfo for a ClusterMember.
type entityTypeClusterMember struct {
	entityTypeCommon
}

func (e entityTypeClusterMember) code() int64 {
	return entityTypeCodeClusterMember
}

func (e entityTypeClusterMember) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, nodes.id, '', '', json_array(nodes.name) FROM nodes`, e.code())
}

func (e entityTypeClusterMember) urlsByIDsQuery(ids ...int64) string {
	return e.allURLsQuery() + " WHERE nodes.id IN " + query.IntParams(ids...)
}

func (e entityTypeClusterMember) idFromURLQuery() string {
	return `
SELECT ?, nodes.id 
FROM nodes 
WHERE '' = ? 
	AND '' = ? 
	AND nodes.name = ?`
}

func (e entityTypeClusterMember) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_node_delete", "nodes", e.code())
}
