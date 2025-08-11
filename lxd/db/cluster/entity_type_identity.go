package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// entityTypeIdentity implements entityTypeDBInfo for an Identity.
type entityTypeIdentity struct {
	entityTypeCommon
}

// identityTypes returns the list of identity type codes that are considered fine-grained.
func (e entityTypeIdentity) identityTypes() (types []int64) {
	for _, t := range identity.Types() {
		if t.IsFineGrained() {
			types = append(types, t.Code())
		}
	}

	return types
}

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
		%s,
		identities.identifier
	)
FROM identities
WHERE type IN %s`,
		e.code(),
		authMethodCaseClause(),
		query.IntParams(e.identityTypes()...))
}

func (e entityTypeIdentity) urlByIDQuery() string {
	return e.allURLsQuery() + " AND identities.id = ?"
}

func (e entityTypeIdentity) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, identities.id
FROM identities
WHERE '' = ?
	AND '' = ?
	AND %s = ?
	AND identities.identifier = ?
	AND identities.type IN %s`,
		authMethodCaseClause(),
		query.IntParams(e.identityTypes()...),
	)
}

func (e entityTypeIdentity) onDeleteTriggerSQL() (name string, sql string) {
	typeCertificate := entityTypeCertificate{}
	name = "on_identity_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON identities
	BEGIN
	DELETE FROM auth_groups_permissions
		WHERE entity_type IN (%d, %d)
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code IN (%d, %d)
		AND entity_id = OLD.id;
	END
`, name, e.code(), typeCertificate.code(), e.code(), typeCertificate.code())
}

// authMethodCaseClause returns the SQL CASE clause for auth method mapping.
func authMethodCaseClause() string {
	return fmt.Sprintf(`CASE identities.auth_method
		WHEN %d THEN '%s'
		WHEN %d THEN '%s'
	END`, authMethodTLS, api.AuthenticationMethodTLS, authMethodOIDC, api.AuthenticationMethodOIDC)
}
