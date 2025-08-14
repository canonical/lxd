package cluster

import (
	"fmt"
)

// entityTypeClusterLink implements entityTypeDBInfo for a [ClusterLink].
type entityTypeClusterLink struct{}

func (e entityTypeClusterLink) code() int64 {
	return entityTypeCodeClusterLink
}

func (e entityTypeClusterLink) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, cluster_links.id, '', '', json_array(cluster_links.name) FROM cluster_links`, e.code())
}

func (e entityTypeClusterLink) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeClusterLink) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE cluster_links.id = ?"
}

func (e entityTypeClusterLink) idFromURLQuery() string {
	return `
SELECT ?, cluster_links.id 
FROM cluster_links 
WHERE '' = ? 
	AND '' = ? 
	AND cluster_links.name = ?`
}

func (e entityTypeClusterLink) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_cluster_link_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON cluster_links
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
