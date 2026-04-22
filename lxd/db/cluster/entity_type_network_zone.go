package cluster

import (
	"fmt"
)

// entityTypeNetworkZone implements entityTypeDBInfo for a NetworkZone.
type entityTypeNetworkZone struct {
	entityTypeCommon
}

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
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeNetworkZone) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE networks_zones.id = ?"
}

func (e entityTypeNetworkZone) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("networks_zones")
}

func (e entityTypeNetworkZone) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_network_zone_delete", "networks_zones", e.code())
}
