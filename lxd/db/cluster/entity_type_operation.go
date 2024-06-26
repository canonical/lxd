package cluster

import (
	"fmt"
	"strings"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeOperation implements entityType for an Operation.
type entityTypeOperation struct {
	entity.Operation
}

// Code returns entityTypeCodeOperation.
func (e entityTypeOperation) Code() int64 {
	return entityTypeCodeOperation
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeOperation, the ID of the Operation,
// the project name of the Operation, the location of the Operation, and the path arguments of the
// Operation in the order that they are found in its URL.
func (e entityTypeOperation) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, operations.id, coalesce(projects.name, ''), '', json_array(operations.uuid) 
FROM operations 
LEFT JOIN projects ON operations.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeOperation) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(e.AllURLsQuery(), "LEFT JOIN projects", "JOIN projects", 1))
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeOperation) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE operations.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeOperation) IDFromURLQuery() string {
	return `
SELECT ?, operations.id 
FROM operations 
LEFT JOIN projects ON operations.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND operations.uuid = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Operation are deleted.
func (e entityTypeOperation) OnDeleteTriggerName() string {
	return "on_operation_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Operation are deleted.
func (e entityTypeOperation) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON operations
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
