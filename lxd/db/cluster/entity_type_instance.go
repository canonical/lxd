package cluster

import (
	"fmt"
)

// entityTypeInstance implements entityTypeDBInfo for an Instance.
type entityTypeInstance struct {
	entityTypeCommon
}

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
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeInstance) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE instances.id = ?"
}

func (e entityTypeInstance) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("instances")
}

func (e entityTypeInstance) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_instance_delete", "instances", e.code())
}
