package cluster

import (
	"fmt"
)

// entityTypeProject implements entityTypeDBInfo for a Project.
type entityTypeProject struct{}

func (e entityTypeProject) code() int64 {
	return entityTypeCodeProject
}

func (e entityTypeProject) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, projects.id, projects.name, '', json_array(projects.name) FROM projects`, e.code())
}

func (e entityTypeProject) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeProject) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE id = ?`, e.allURLsQuery())
}

func (e entityTypeProject) idFromURLQuery() string {
	return `
SELECT ?, projects.id 
FROM projects 
WHERE projects.name = ? 
	AND '' = ? 
	AND projects.name = ?`
}

func (e entityTypeProject) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_project_delete"
	return name, fmt.Sprintf(`
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
`, name, e.code(), e.code())
}
