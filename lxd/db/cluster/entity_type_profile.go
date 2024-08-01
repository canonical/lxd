package cluster

import (
	"fmt"
)

// entityTypeProfile implements entityTypeDBInfo for a Profile.
type entityTypeProfile struct{}

func (e entityTypeProfile) code() int64 {
	return entityTypeCodeProfile
}

func (e entityTypeProfile) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, profiles.id, projects.name, '', json_array(profiles.name) 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id`, e.code())
}

func (e entityTypeProfile) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeProfile) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE profiles.id = ?`, e.allURLsQuery())
}

func (e entityTypeProfile) idFromURLQuery() string {
	return `
SELECT ?, profiles.id 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND profiles.name = ?`
}

func (e entityTypeProfile) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_profile_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON profiles
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
