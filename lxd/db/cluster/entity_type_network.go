package cluster

import (
	"fmt"
)

// entityTypeNetwork implements entityTypeDBInfo for a Network.
type entityTypeNetwork struct {
	entityTypeCommon
}

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
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeNetwork) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE networks.id = ?"
}

func (e entityTypeNetwork) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("networks")
}

func (e entityTypeNetwork) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_network_delete", "networks", e.code())
}
