package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeProject implements entityType for a Project.
type entityTypeProject struct {
	entity.Project
}

// Code returns entityTypeCodeProject.
func (e entityTypeProject) Code() int64 {
	return entityTypeCodeProject
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeProject, the ID of the Project,
// the project name of the Project, the location of the Project, and the path arguments of the
// Project in the order that they are found in its URL.
func (e entityTypeProject) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, projects.id, '', '', json_array(projects.name) FROM projects`, e.Code())
}

// URLsByProjectQuery returns an empty string because Project entities are not project specific.
func (e entityTypeProject) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeProject) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeProject) IDFromURLQuery() string {
	return `
SELECT ?, projects.id 
FROM projects 
WHERE '' = ? 
	AND '' = ? 
	AND projects.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Project are deleted.
func (e entityTypeProject) OnDeleteTriggerName() string {
	return "on_project_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Project are deleted.
func (e entityTypeProject) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON projects
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
