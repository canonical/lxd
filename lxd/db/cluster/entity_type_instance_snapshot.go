package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeInstanceSnapshot implements entityType for an InstanceSnapshot.
type entityTypeInstanceSnapshot struct {
	entity.InstanceSnapshot
}

// Code returns entityTypeCodeInstanceSnapshot.
func (e entityTypeInstanceSnapshot) Code() int64 {
	return entityTypeCodeInstanceSnapshot
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeInstanceSnapshot, the ID of the InstanceSnapshot,
// the project name of the InstanceSnapshot, the location of the InstanceSnapshot, and the path arguments of the
// InstanceSnapshot in the order that they are found in its URL.
func (e entityTypeInstanceSnapshot) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances_snapshots.id, projects.name, '', json_array(instances.name, instances_snapshots.name) 
FROM instances_snapshots 
JOIN instances ON instances_snapshots.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeInstanceSnapshot) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeInstanceSnapshot) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeInstanceSnapshot) IDFromURLQuery() string {
	return `
SELECT ?, instances_snapshots.id 
FROM instances_snapshots 
JOIN instances ON instances_snapshots.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances_snapshots.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type InstanceSnapshot are deleted.
func (e entityTypeInstanceSnapshot) OnDeleteTriggerName() string {
	return "on_instance_snaphot_delete" // TODO: Spelling was wrong originally. We need a patch to fix this.
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type InstanceSnapshot are deleted.
func (e entityTypeInstanceSnapshot) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON instances_snapshots
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
