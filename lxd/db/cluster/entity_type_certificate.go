package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/identity"
)

// entityTypeCertificate implements entityTypeDBInfo for a Certificate.
type entityTypeCertificate struct{}

var certIdentityTypes = func() (result []int64) {
	for _, identityType := range identity.Types() {
		_, err := identityType.LegacyCertificateType()
		if err == nil {
			result = append(result, identityType.Code())
		}
	}

	return result
}

func (e entityTypeCertificate) code() int64 {
	return entityTypeCodeCertificate
}

func (e entityTypeCertificate) allURLsQuery() string {
	return fmt.Sprintf(
		`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d AND type IN %s`,
		e.code(),
		authMethodTLS,
		typeInClause(certIdentityTypes()...))
}

func (e entityTypeCertificate) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeCertificate) urlByIDQuery() string {
	return e.allURLsQuery() + " AND identities.id = ?"
}

func (e entityTypeCertificate) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND identities.identifier = ? 
	AND identities.auth_method = %d
	AND identities.type IN %s
`, authMethodTLS,
		typeInClause(certIdentityTypes()...))
}

func (e entityTypeCertificate) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}
