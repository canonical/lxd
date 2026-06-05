package cluster

import (
	"fmt"
)

// entityTypeOperation implements entityTypeDBInfo for an Operation.
type entityTypeOperation struct {
	entityTypeCommon
}

func (e entityTypeOperation) code() int64 {
	return entityTypeCodeOperation
}

func (e entityTypeOperation) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, operations.id, coalesce(projects.name, ''), '', json_array(operations.uuid)
FROM operations
LEFT JOIN projects ON operations.project_id = projects.id`, e.code())
}

func (e entityTypeOperation) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeOperation) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE operations.id = ?"
}

func (e entityTypeOperation) idFromURLQuery() string {
	return `
SELECT ?, operations.id
FROM operations
WHERE '' = ?
	AND '' = ?
	AND operations.uuid = ?`
}

func (e entityTypeOperation) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_operation_delete", "operations", e.code())
}
