package cluster

import (
	"fmt"
)

// entityTypeInstanceSnapshot implements entityTypeDBInfo for an InstanceSnapshot.
type entityTypeInstanceSnapshot struct {
	entityTypeCommon
}

func (e entityTypeInstanceSnapshot) code() int64 {
	return entityTypeCodeInstanceSnapshot
}

func (e entityTypeInstanceSnapshot) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances_snapshots.id, projects.name, '', json_array(instances.name, instances_snapshots.name) 
FROM instances_snapshots 
JOIN instances ON instances_snapshots.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id`, e.code())
}

func (e entityTypeInstanceSnapshot) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeInstanceSnapshot) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeInstanceSnapshot) idFromURLQuery() string {
	return `
SELECT ?, instances_snapshots.id 
FROM instances_snapshots 
JOIN instances ON instances_snapshots.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances_snapshots.name = ?`
}

func (e entityTypeInstanceSnapshot) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_instance_snapshot_delete", "instances_snapshots", e.code())
}
