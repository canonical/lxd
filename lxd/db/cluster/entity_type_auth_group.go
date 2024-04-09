package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeAuthGroup implements entityType for an AuthGroup.
type entityTypeAuthGroup struct {
	entity.AuthGroup
}

// Code returns entityTypeCodeAuthGroup.
func (e entityTypeAuthGroup) Code() int64 {
	return entityTypeCodeAuthGroup
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeAuthGroup, the ID of the AuthGroup,
// the project name of the AuthGroup, the location of the AuthGroup, and the path arguments of the
// AuthGroup in the order that they are found in its URL.
func (e entityTypeAuthGroup) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, auth_groups.id, '', '', json_array(auth_groups.name) FROM auth_groups`, e.Code())
}

// URLsByProjectQuery returns an empty string because AuthGroup entities are not project specific.
func (e entityTypeAuthGroup) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeAuthGroup) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE auth_groups.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeAuthGroup) IDFromURLQuery() string {
	return `
SELECT ?, auth_groups.id 
FROM auth_groups 
WHERE '' = ? 
	AND '' = ? 
	AND auth_groups.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type AuthGroup are deleted.
func (e entityTypeAuthGroup) OnDeleteTriggerName() string {
	return "on_auth_group_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type AuthGroup are deleted.
func (e entityTypeAuthGroup) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON auth_groups
	BEGIN
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, e.OnDeleteTriggerName(), e.Code())
}
