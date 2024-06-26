package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeIdentityProviderGroup implements entityType for an IdentityProviderGroup.
type entityTypeIdentityProviderGroup struct {
	entity.IdentityProviderGroup
}

// Code returns entityTypeCodeIdentityProviderGroup.
func (e entityTypeIdentityProviderGroup) Code() int64 {
	return entityTypeCodeIdentityProviderGroup
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeIdentityProviderGroup, the ID of the IdentityProviderGroup,
// the project name of the IdentityProviderGroup, the location of the IdentityProviderGroup, and the path arguments of the
// IdentityProviderGroup in the order that they are found in its URL.
func (e entityTypeIdentityProviderGroup) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, identity_provider_groups.id, '', '', json_array(identity_provider_groups.name) FROM identity_provider_groups`, e.Code())
}

// URLsByProjectQuery returns an empty string because IdentityProviderGroup entities are not project specific.
func (e entityTypeIdentityProviderGroup) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeIdentityProviderGroup) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE identity_provider_groups.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeIdentityProviderGroup) IDFromURLQuery() string {
	return `
SELECT ?, identity_provider_groups.id 
FROM identity_provider_groups 
WHERE '' = ? 
	AND '' = ? 
	AND identity_provider_groups.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type IdentityProviderGroup are deleted.
func (e entityTypeIdentityProviderGroup) OnDeleteTriggerName() string {
	return "on_identity_provider_group_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type IdentityProviderGroup are deleted.
func (e entityTypeIdentityProviderGroup) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON identity_provider_groups
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
