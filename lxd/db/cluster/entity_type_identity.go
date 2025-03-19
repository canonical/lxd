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
WHERE type IN (%d, %d, %d, %d, %d)
`,
		e.code(),
		authMethodTLS, api.AuthenticationMethodTLS,
		authMethodOIDC, api.AuthenticationMethodOIDC,
		identityTypeOIDCClient, identityTypeCertificateClient, identityTypeCertificateClientPending, identityTypeCertificateClusterLink, identityTypeCertificateClusterLinkPending,
	)
}

func (e entityTypeIdentity) urlsByProjectQuery() string {
	return ""
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
	AND CASE identities.auth_method 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND identities.identifier = ?
	AND identities.type IN (%d, %d, %d, %d, %d)
`, authMethodTLS, api.AuthenticationMethodTLS,
		authMethodOIDC, api.AuthenticationMethodOIDC,
		identityTypeOIDCClient, identityTypeCertificateClient, identityTypeCertificateClientPending, identityTypeCertificateClusterLink, identityTypeCertificateClusterLinkPending,
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
