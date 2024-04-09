package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeClusterGroup implements entityType for a ClusterGroup.
type entityTypeClusterGroup struct {
	entity.ClusterGroup
}

// Code returns entityTypeCodeClusterGroup.
func (e entityTypeClusterGroup) Code() int64 {
	return entityTypeCodeClusterGroup
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeClusterGroup, the ID of the ClusterGroup,
// the project name of the ClusterGroup, the location of the ClusterGroup, and the path arguments of the
// ClusterGroup in the order that they are found in its URL.
func (e entityTypeClusterGroup) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, cluster_groups.id, '', '', json_array(cluster_groups.name) FROM cluster_groups`, e.Code())
}

// URLsByProjectQuery returns an empty string because ClusterGroup entities are not project specific.
func (e entityTypeClusterGroup) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeClusterGroup) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE cluster_groups.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeClusterGroup) IDFromURLQuery() string {
	return `
SELECT ?, cluster_groups.id 
FROM cluster_groups 
WHERE '' = ? 
	AND '' = ? 
	AND cluster_groups.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type ClusterGroup are deleted.
func (e entityTypeClusterGroup) OnDeleteTriggerName() string {
	return "on_cluster_group_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type ClusterGroup are deleted.
func (e entityTypeClusterGroup) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
