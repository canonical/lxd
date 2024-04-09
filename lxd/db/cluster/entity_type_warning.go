package cluster

import (
	"fmt"
	"strings"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeWarning implements entityType for a Warning.
type entityTypeWarning struct {
	entity.Warning
}

// Code returns entityTypeCodeWarning.
func (e entityTypeWarning) Code() int64 {
	return entityTypeCodeWarning
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeWarning, the ID of the Warning,
// the project name of the Warning, the location of the Warning, and the path arguments of the
// Warning in the order that they are found in its URL.
func (e entityTypeWarning) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, warnings.id, coalesce(projects.name, ''), replace(coalesce(nodes.name, ''), 'none', ''), json_array(warnings.uuid) 
FROM warnings 
LEFT JOIN projects ON warnings.project_id = projects.id 
LEFT JOIN nodes ON warnings.node_id = nodes.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeWarning) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(e.AllURLsQuery(), "LEFT JOIN projects", "JOIN projects", 1))
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeWarning) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE warnings.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeWarning) IDFromURLQuery() string {
	return `
SELECT ?, warnings.id 
FROM warnings 
LEFT JOIN projects ON warnings.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND warnings.uuid = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Warning are deleted.
func (e entityTypeWarning) OnDeleteTriggerName() string {
	return "on_warning_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Warning are deleted.
func (e entityTypeWarning) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON warnings
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	END
`, e.OnDeleteTriggerName(), e.Code())
}
