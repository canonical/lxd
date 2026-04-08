package cluster

import (
	"fmt"
)

// entityTypeInstanceBackup implements entityTypeDBInfo for an InstanceBackup.
//
// Note: instances_backups.name stores the full prefixed name "instanceName/backupName"
// (e.g. "c1/backup0"), unlike instance snapshots which store just the snapshot name.
// The queries below must account for this by stripping the prefix in allURLsQuery
// and concatenating it back in idFromURLQuery.
type entityTypeInstanceBackup struct {
	entityTypeCommon
}

func (e entityTypeInstanceBackup) code() int64 {
	return entityTypeCodeInstanceBackup
}

func (e entityTypeInstanceBackup) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances_backups.id, projects.name, '', json_array(instances.name, substr(instances_backups.name, length(instances.name) + 2))
FROM instances_backups
JOIN instances ON instances_backups.instance_id = instances.id
JOIN projects ON instances.project_id = projects.id`, e.code())
}

func (e entityTypeInstanceBackup) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeInstanceBackup) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE instances_backups.id = ?"
}

func (e entityTypeInstanceBackup) idFromURLQuery() string {
	return `
SELECT ?, instances_backups.id 
FROM instances_backups 
JOIN instances ON instances_backups.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ?
	AND instances_backups.name = instances.name || '/' || ?`
}

func (e entityTypeInstanceBackup) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_instance_backup_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON instances_backups
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
