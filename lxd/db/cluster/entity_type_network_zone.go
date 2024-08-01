package cluster

import (
	"fmt"
)

// entityTypeNetworkZone implements entityTypeDBInfo for a NetworkZone.
type entityTypeNetworkZone struct{}

func (e entityTypeNetworkZone) code() int64 {
	return entityTypeCodeNetworkZone
}

func (e entityTypeNetworkZone) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks_zones.id, projects.name, '', json_array(networks_zones.name) 
FROM networks_zones 
JOIN projects ON networks_zones.project_id = projects.id`, e.code())
}

func (e entityTypeNetworkZone) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeNetworkZone) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks_zones.id = ?`, e.allURLsQuery())
}

func (e entityTypeNetworkZone) idFromURLQuery() string {
	return `
SELECT ?, networks_zones.id 
FROM networks_zones 
JOIN projects ON networks_zones.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_zones.name = ?`
}

func (e entityTypeNetworkZone) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_network_zone_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON networks_zones
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
