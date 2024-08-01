package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
)

// entityTypeIdentity implements entityTypeDBInfo for an Identity.
type entityTypeIdentity struct{}

func (e entityTypeIdentity) code() int64 {
	return entityTypeCodeIdentity
}

func (e entityTypeIdentity) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT 
	%d, 
	identities.id, 
	'', 
	'', 
	json_array(
		CASE identities.auth_method
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		identities.identifier
	) 
FROM identities
`,
		e.code(),
		authMethodTLS, api.AuthenticationMethodTLS,
		authMethodOIDC, api.AuthenticationMethodOIDC,
	)
}

func (e entityTypeIdentity) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeIdentity) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE identities.id = ?`, e.allURLsQuery())
}

func (e entityTypeIdentity) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND CASE identities.auth_method 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND identities.identifier = ?
`, authMethodTLS, api.AuthenticationMethodTLS,
		authMethodOIDC, api.AuthenticationMethodOIDC)
}

func (e entityTypeIdentity) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_identity_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON identities
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
