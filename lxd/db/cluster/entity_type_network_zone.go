package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeNetworkZone implements entityType for a NetworkZone.
type entityTypeNetworkZone struct {
	entity.NetworkZone
}

// Code returns entityTypeCodeNetworkZone.
func (e entityTypeNetworkZone) Code() int64 {
	return entityTypeCodeNetworkZone
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeNetworkZone, the ID of the NetworkZone,
// the project name of the NetworkZone, the location of the NetworkZone, and the path arguments of the
// NetworkZone in the order that they are found in its URL.
func (e entityTypeNetworkZone) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks_zones.id, projects.name, '', json_array(networks_zones.name) 
FROM networks_zones 
JOIN projects ON networks_zones.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeNetworkZone) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeNetworkZone) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks_zones.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeNetworkZone) IDFromURLQuery() string {
	return `
SELECT ?, networks_zones.id 
FROM networks_zones 
JOIN projects ON networks_zones.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_zones.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type NetworkZone are deleted.
func (e entityTypeNetworkZone) OnDeleteTriggerName() string {
	return "on_network_zone_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type NetworkZone are deleted.
func (e entityTypeNetworkZone) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
