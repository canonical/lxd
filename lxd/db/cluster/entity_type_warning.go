package cluster

import (
	"fmt"
	"strings"
)

// entityTypeWarning implements entityTypeDBInfo for a Warning.
type entityTypeWarning struct{}

func (e entityTypeWarning) code() int64 {
	return entityTypeCodeWarning
}

func (e entityTypeWarning) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, warnings.id, coalesce(projects.name, ''), replace(coalesce(nodes.name, ''), 'none', ''), json_array(warnings.uuid) 
FROM warnings 
LEFT JOIN projects ON warnings.project_id = projects.id 
LEFT JOIN nodes ON warnings.node_id = nodes.id`, e.code())
}

func (e entityTypeWarning) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(e.allURLsQuery(), "LEFT JOIN projects", "JOIN projects", 1))
}

func (e entityTypeWarning) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE warnings.id = ?`, e.allURLsQuery())
}

func (e entityTypeWarning) idFromURLQuery() string {
	return `
SELECT ?, warnings.id 
FROM warnings 
LEFT JOIN projects ON warnings.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND warnings.uuid = ?`
}

func (e entityTypeWarning) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_warning_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON warnings
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	END
`, name, e.code())
}
