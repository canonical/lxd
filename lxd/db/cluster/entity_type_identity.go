package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/identity"
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
	DELETE FROM secrets
		WHERE entity_type = %d
		AND entity_id = OLD.id;
	END
`, name, e.code(), typeCertificate.code(), e.code(), typeCertificate.code(), e.code())
}

// authMethodCaseClause returns the SQL CASE clause for auth method mapping.
func authMethodCaseClause() string {
	var b strings.Builder
	b.WriteString(`CASE identities.auth_method `)
	for code, text := range authMethodCodeToText {
		codeStr := strconv.FormatInt(code, 10)
		b.WriteString(`WHEN ` + codeStr + ` THEN '` + text + `' `)
	}

	b.WriteString(`END`)
	return b.String()
}

// onInsertTriggerSQL enforces that newly created identities have a unique name within the authentication method (where
// method is not OIDC).
func (e entityTypeIdentity) onInsertTriggerSQL() (name string, sql string) {
	name = "on_identity_insert"
	sql = `CREATE TRIGGER ` + name + `
	BEFORE INSERT ON identities
	WHEN NEW.auth_method != ` + strconv.FormatInt(authMethodOIDC, 10) + `
		AND (SELECT COUNT(*) FROM identities WHERE name = NEW.name AND auth_method = NEW.auth_method) > 0
	BEGIN
		SELECT RAISE(ABORT, 'An identity with this name and authentication method already exists');
	END`

	return name, sql
}

// onUpdateTriggerSQL enforces that identities whose authentication method is not OIDC have a unique name (within
// identities using that authentication method). This trigger only runs if the name of the identity is being changed,
// this allows pre-existing identities with duplicated names to continue to update normally.
func (e entityTypeIdentity) onUpdateTriggerSQL() (name string, sql string) {
	name = "on_identity_update"
	sql = `CREATE TRIGGER ` + name + `
	BEFORE UPDATE ON identities
	WHEN OLD.name != NEW.name AND NEW.auth_method != ` + strconv.FormatInt(authMethodOIDC, 10) + `
		AND (SELECT COUNT(*) FROM identities WHERE name = NEW.name AND auth_method = NEW.auth_method) > 0
	BEGIN
		SELECT RAISE(ABORT, 'An identity with this name and authentication method already exists');
	END`

	return name, sql
}
