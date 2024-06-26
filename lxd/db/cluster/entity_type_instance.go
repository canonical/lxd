package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeInstance implements entityType for an Instance.
type entityTypeInstance struct {
	entity.Instance
}

// Code returns entityTypeCodeInstance.
func (e entityTypeInstance) Code() int64 {
	return entityTypeCodeInstance
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeInstance, the ID of the Instance,
// the project name of the Instance, the location of the Instance, and the path arguments of the
// Instance in the order that they are found in its URL.
func (e entityTypeInstance) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances.id, projects.name, '', json_array(instances.name) 
FROM instances 
JOIN projects ON instances.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeInstance) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeInstance) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE instances.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeInstance) IDFromURLQuery() string {
	return `
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Instance are deleted.
func (e entityTypeInstance) OnDeleteTriggerName() string {
	return "on_instance_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Instance are deleted.
func (e entityTypeInstance) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON instances
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
