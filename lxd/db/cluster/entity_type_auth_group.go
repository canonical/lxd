package cluster

import (
	"fmt"
)

// entityTypeAuthGroup implements entityTypeDBInfo for an AuthGroup.
type entityTypeAuthGroup struct{}

func (e entityTypeAuthGroup) code() int64 {
	return entityTypeCodeAuthGroup
}

func (e entityTypeAuthGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, auth_groups.id, '', '', json_array(auth_groups.name) FROM auth_groups`, e.code())
}

func (e entityTypeAuthGroup) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeAuthGroup) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE auth_groups.id = ?`, e.allURLsQuery())
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
	name = "on_auth_group_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON auth_groups
	BEGIN
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, name, e.code())
}
