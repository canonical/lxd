package cluster

import (
	"fmt"
)

// entityTypeIdentityProviderGroup implements entityTypeDBInfo for an IdentityProviderGroup.
type entityTypeIdentityProviderGroup struct {
	entityTypeCommon
}

func (e entityTypeIdentityProviderGroup) code() int64 {
	return entityTypeCodeIdentityProviderGroup
}

func (e entityTypeIdentityProviderGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, identity_provider_groups.id, '', '', json_array(identity_provider_groups.name) FROM identity_provider_groups`, e.code())
}

func (e entityTypeIdentityProviderGroup) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE identity_provider_groups.id = ?"
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
	return standardOnDeleteTriggerSQL("on_identity_provider_group_delete", "identity_provider_groups", e.code())
}
