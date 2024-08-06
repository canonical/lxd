package cluster

import (
	"fmt"
)

// entityTypeInstanceSnapshot implements entityTypeDBInfo for an InstanceSnapshot.
type entityTypeInstanceSnapshot struct{}

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
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeInstanceSnapshot) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
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
	name = "on_instance_snaphot_delete" // TODO: Spelling was wrong originally. We need a patch to fix this.
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON instances_snapshots
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
