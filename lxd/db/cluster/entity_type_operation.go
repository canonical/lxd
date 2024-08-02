package cluster

import (
	"fmt"
	"strings"
)

// entityTypeOperation implements entityTypeDBInfo for an Operation.
type entityTypeOperation struct{}

func (e entityTypeOperation) code() int64 {
	return entityTypeCodeOperation
}

func (e entityTypeOperation) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, operations.id, coalesce(projects.name, ''), '', json_array(operations.uuid) 
FROM operations 
LEFT JOIN projects ON operations.project_id = projects.id`, e.code())
}

func (e entityTypeOperation) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(e.allURLsQuery(), "LEFT JOIN projects", "JOIN projects", 1))
}

func (e entityTypeOperation) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE operations.id = ?`, e.allURLsQuery())
}

func (e entityTypeOperation) idFromURLQuery() string {
	return `
SELECT ?, operations.id 
FROM operations 
LEFT JOIN projects ON operations.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND operations.uuid = ?`
}

func (e entityTypeOperation) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_operation_delete"
	return name, fmt.Sprintf(`
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
`, name, e.code(), e.code())
}
