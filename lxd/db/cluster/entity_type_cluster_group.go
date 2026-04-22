package cluster

import (
	"fmt"
)

// entityTypeClusterGroup implements entityTypeDBInfo for a ClusterGroup.
type entityTypeClusterGroup struct {
	entityTypeCommon
}

func (e entityTypeClusterGroup) code() int64 {
	return entityTypeCodeClusterGroup
}

func (e entityTypeClusterGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, cluster_groups.id, '', '', json_array(cluster_groups.name) FROM cluster_groups`, e.code())
}

func (e entityTypeClusterGroup) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE cluster_groups.id = ?"
}

func (e entityTypeClusterGroup) idFromURLQuery() string {
	return `
SELECT ?, cluster_groups.id 
FROM cluster_groups 
WHERE '' = ? 
	AND '' = ? 
	AND cluster_groups.name = ?`
}

func (e entityTypeClusterGroup) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_cluster_group_delete", "cluster_groups", e.code())
}
