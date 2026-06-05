package cluster

import (
	"fmt"
)

// entityTypeWarning implements entityTypeDBInfo for a Warning.
type entityTypeWarning struct {
	entityTypeCommon
}

func (e entityTypeWarning) code() int64 {
	return entityTypeCodeWarning
}

func (e entityTypeWarning) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, warnings.id, coalesce(projects.name, ''), '', json_array(warnings.uuid)
FROM warnings
LEFT JOIN projects ON warnings.project_id = projects.id`, e.code())
}

func (e entityTypeWarning) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeWarning) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE warnings.id = ?"
}

func (e entityTypeWarning) idFromURLQuery() string {
	return `
SELECT ?, warnings.id
FROM warnings
WHERE '' = ?
	AND '' = ?
	AND warnings.uuid = ?`
}

func (e entityTypeWarning) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_warning_delete", "warnings", e.code())
}
