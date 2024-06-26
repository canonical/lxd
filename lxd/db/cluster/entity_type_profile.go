package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeProfile implements entityType for a Profile.
type entityTypeProfile struct {
	entity.Profile
}

// Code returns entityTypeCodeProfile.
func (e entityTypeProfile) Code() int64 {
	return entityTypeCodeProfile
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeProfile, the ID of the Profile,
// the project name of the Profile, the location of the Profile, and the path arguments of the
// Profile in the order that they are found in its URL.
func (e entityTypeProfile) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, profiles.id, projects.name, '', json_array(profiles.name) 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeProfile) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeProfile) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE profiles.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeProfile) IDFromURLQuery() string {
	return `
SELECT ?, profiles.id 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND profiles.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Profile are deleted.
func (e entityTypeProfile) OnDeleteTriggerName() string {
	return "on_profile_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Profile are deleted.
func (e entityTypeProfile) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
