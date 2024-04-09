package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeClusterMember implements entityType for a ClusterMember.
type entityTypeClusterMember struct {
	entity.ClusterMember
}

// Code returns entityTypeCodeClusterMember.
func (e entityTypeClusterMember) Code() int64 {
	return entityTypeCodeClusterMember
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeClusterMember, the ID of the ClusterMember,
// the project name of the ClusterMember, the location of the ClusterMember, and the path arguments of the
// ClusterMember in the order that they are found in its URL.
func (e entityTypeClusterMember) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, nodes.id, '', '', json_array(nodes.name) FROM nodes`, e.Code())
}

// URLsByProjectQuery returns an empty string because ClusterMember entities are not project specific.
func (e entityTypeClusterMember) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeClusterMember) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE nodes.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeClusterMember) IDFromURLQuery() string {
	return `
SELECT ?, nodes.id 
FROM nodes 
WHERE '' = ? 
	AND '' = ? 
	AND nodes.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type ClusterMember are deleted.
func (e entityTypeClusterMember) OnDeleteTriggerName() string {
	return "on_node_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type ClusterMember are deleted.
func (e entityTypeClusterMember) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
