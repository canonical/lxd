package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/identity"
)

// entityTypeCertificate implements entityTypeDBInfo for a Certificate.
type entityTypeCertificate struct {
	entityTypeCommon
}

var certIdentityTypes = func() (types []int64) {
	for _, t := range identity.Types() {
		_, err := t.LegacyCertificateType()
		if err == nil {
			types = append(types, t.Code())
		}
	}

	return types
}

func (e entityTypeCertificate) code() int64 {
	return entityTypeCodeCertificate
}

func (e entityTypeCertificate) allURLsQuery() string {
	return fmt.Sprintf(
		`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d AND type IN %s`,
		e.code(),
		authMethodTLS,
		query.IntParams(certIdentityTypes()...))
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
		query.IntParams(certIdentityTypes()...))
}
