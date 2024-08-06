package cluster

import (
	"fmt"
)

// entityTypeInstance implements entityTypeDBInfo for an Instance.
type entityTypeInstance struct{}

func (e entityTypeInstance) code() int64 {
	return entityTypeCodeInstance
}

func (e entityTypeInstance) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances.id, projects.name, '', json_array(instances.name) 
FROM instances 
JOIN projects ON instances.project_id = projects.id`, e.code())
}

func (e entityTypeInstance) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeInstance) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE instances.id = ?`, e.allURLsQuery())
}

func (e entityTypeInstance) idFromURLQuery() string {
	return `
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ?`
}

func (e entityTypeInstance) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_instance_delete"
	return name, fmt.Sprintf(`
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
`, name, e.code(), e.code())
}
