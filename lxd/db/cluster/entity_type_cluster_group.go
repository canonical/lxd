package cluster

import (
	"fmt"
)

// entityTypeClusterGroup implements entityTypeDBInfo for a ClusterGroup.
type entityTypeClusterGroup struct{}

func (e entityTypeClusterGroup) code() int64 {
	return entityTypeCodeClusterGroup
}

func (e entityTypeClusterGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, cluster_groups.id, '', '', json_array(cluster_groups.name) FROM cluster_groups`, e.code())
}

func (e entityTypeClusterGroup) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeClusterGroup) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE cluster_groups.id = ?`, e.allURLsQuery())
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
	name = "on_cluster_group_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON cluster_groups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, name, e.code(), e.code())
}
