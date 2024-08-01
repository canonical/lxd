package cluster

import (
	"fmt"
)

// entityTypeNetworkACL implements entityTypeDBInfo for a NetworkACL.
type entityTypeNetworkACL struct{}

func (e entityTypeNetworkACL) code() int64 {
	return entityTypeCodeNetworkACL
}

func (e entityTypeNetworkACL) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks_acls.id, projects.name, '', json_array(networks_acls.name) 
FROM networks_acls 
JOIN projects ON networks_acls.project_id = projects.id`, e.code())
}

func (e entityTypeNetworkACL) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeNetworkACL) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks_acls.id = ?`, e.allURLsQuery())
}

func (e entityTypeNetworkACL) idFromURLQuery() string {
	return `
SELECT ?, networks_acls.id 
FROM networks_acls 
JOIN projects ON networks_acls.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_acls.name = ?`
}

func (e entityTypeNetworkACL) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_network_acl_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON networks_acls
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
