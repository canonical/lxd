package cluster

import (
	"fmt"
)

// entityTypeCommon acts as a base entityTypeDBInfo.
type entityTypeCommon struct{}

// allURLsQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) allURLsQuery() string {
	return ""
}

// urlsByProjectQuery returns empty because not all entityTypeDBInfo are project specific.
func (e entityTypeCommon) urlsByProjectQuery() string {
	return ""
}

// urlByIDQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) urlByIDQuery() string {
	return ""
}

// idFromURLQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) idFromURLQuery() string {
	return ""
}

// onDeleteTriggerSQL returns empty because not all entityTypeDBInfo implementations have triggers (e.g. entityTypeServer, entityTypeCertificate).
func (e entityTypeCommon) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}

// onInsertTriggerSQL returns empty because most entityTypeDBInfo implementations have an insert trigger.
func (e entityTypeCommon) onInsertTriggerSQL() (name string, sql string) {
	return "", ""
}

// onUpdateTriggerSQL returns empty because most entityTypeDBInfo implementations have an update trigger.
func (e entityTypeCommon) onUpdateTriggerSQL() (name string, sql string) {
	return "", ""
}

// standardOnDeleteTriggerSQL generates the standard AFTER DELETE trigger that cleans up
// auth_groups_permissions and warnings rows when an entity is deleted.
func standardOnDeleteTriggerSQL(triggerName string, tableName string, code int64) (name string, sql string) {
	return triggerName, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON %s
	BEGIN
	DELETE FROM auth_groups_permissions
		WHERE entity_type = %d
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, triggerName, tableName, code, code)
}

// projectEntityIDFromURLQuery generates the standard idFromURLQuery SQL for entities that
// live under a project and are identified by a single name column.
func projectEntityIDFromURLQuery(tableName string) string {
	return fmt.Sprintf(`
SELECT ?, %s.id
FROM %s
JOIN projects ON %s.project_id = projects.id
WHERE projects.name = ?
	AND '' = ?
	AND %s.name = ?`, tableName, tableName, tableName, tableName)
}
