package cluster

import (
	"fmt"
)

// entityTypeClusterMember implements entityTypeDBInfo for a ClusterMember.
type entityTypeClusterMember struct{}

func (e entityTypeClusterMember) code() int64 {
	return entityTypeCodeClusterMember
}

func (e entityTypeClusterMember) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, nodes.id, '', '', json_array(nodes.name) FROM nodes`, e.code())
}

func (e entityTypeClusterMember) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeClusterMember) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE nodes.id = ?`, e.allURLsQuery())
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
	name = "on_node_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON nodes
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
