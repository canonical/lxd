package cluster

import (
	"fmt"
)

// entityTypeIdentityProviderGroup implements entityTypeDBInfo for an IdentityProviderGroup.
type entityTypeIdentityProviderGroup struct{}

func (e entityTypeIdentityProviderGroup) code() int64 {
	return entityTypeCodeIdentityProviderGroup
}

func (e entityTypeIdentityProviderGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, identity_provider_groups.id, '', '', json_array(identity_provider_groups.name) FROM identity_provider_groups`, e.code())
}

func (e entityTypeIdentityProviderGroup) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeIdentityProviderGroup) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE identity_provider_groups.id = ?`, e.allURLsQuery())
}

func (e entityTypeIdentityProviderGroup) idFromURLQuery() string {
	return `
SELECT ?, identity_provider_groups.id 
FROM identity_provider_groups 
WHERE '' = ? 
	AND '' = ? 
	AND identity_provider_groups.name = ?`
}

func (e entityTypeIdentityProviderGroup) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_identity_provider_group_delete"
	return name, fmt.Sprintf(`
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
`, name, e.code(), e.code())
}
