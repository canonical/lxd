package cluster

import (
	"fmt"
)

// entityTypeAuthGroup implements entityTypeDBInfo for an AuthGroup.
type entityTypeAuthGroup struct {
	entityTypeCommon
}

func (e entityTypeAuthGroup) code() int64 {
	return entityTypeCodeAuthGroup
}

func (e entityTypeAuthGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, auth_groups.id, '', '', json_array(auth_groups.name) FROM auth_groups`, e.code())
}

func (e entityTypeAuthGroup) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE auth_groups.id = ?"
}

func (e entityTypeAuthGroup) idFromURLQuery() string {
	return `
SELECT ?, auth_groups.id 
FROM auth_groups 
WHERE '' = ? 
	AND '' = ? 
	AND auth_groups.name = ?`
}

func (e entityTypeAuthGroup) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_auth_group_delete", "auth_groups", e.code())
}
