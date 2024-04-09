package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// entityTypeIdentity implements entityType for an Identity.
type entityTypeIdentity struct {
	entity.Identity
}

// Code returns entityTypeCodeIdentity.
func (e entityTypeIdentity) Code() int64 {
	return entityTypeCodeIdentity
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeIdentity, the ID of the Identity,
// the project name of the Identity, the location of the Identity, and the path arguments of the
// Identity in the order that they are found in its URL.
func (e entityTypeIdentity) AllURLsQuery() string {
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
		e.Code(),
		authMethodTLS, api.AuthenticationMethodTLS,
		authMethodOIDC, api.AuthenticationMethodOIDC,
	)
}

// URLsByProjectQuery returns an empty string because Identity entities are not project specific.
func (e entityTypeIdentity) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeIdentity) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE identities.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeIdentity) IDFromURLQuery() string {
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

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Identity are deleted.
func (e entityTypeIdentity) OnDeleteTriggerName() string {
	return "on_identity_delete"
}

// OnDeleteTriggerSQL returns SQL that creates a trigger that is run when entities of type Identity are deleted.
func (e entityTypeIdentity) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
