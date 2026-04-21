package cluster

import (
	"fmt"
)

// entityTypeNetworkACL implements entityTypeDBInfo for a NetworkACL.
type entityTypeNetworkACL struct {
	entityTypeCommon
}

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
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeNetworkACL) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE networks_acls.id = ?"
}

func (e entityTypeNetworkACL) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("networks_acls")
}

func (e entityTypeNetworkACL) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_network_acl_delete", "networks_acls", e.code())
}
