package cluster

import (
	"fmt"
)

// entityTypeNetwork implements entityTypeDBInfo for a Network.
type entityTypeNetwork struct{}

func (e entityTypeNetwork) code() int64 {
	return entityTypeCodeNetwork
}

func (e entityTypeNetwork) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks.id, projects.name, '', json_array(networks.name) 
FROM networks 
JOIN projects ON networks.project_id = projects.id`, e.code())
}

func (e entityTypeNetwork) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeNetwork) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks.id = ?`, e.allURLsQuery())
}

func (e entityTypeNetwork) idFromURLQuery() string {
	return `
SELECT ?, networks.id 
FROM networks 
JOIN projects ON networks.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks.name = ?`
}

func (e entityTypeNetwork) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_network_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON networks
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
