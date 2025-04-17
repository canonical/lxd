package cluster

import (
	"fmt"
)

// entityTypeCertificate implements entityTypeDBInfo for a Certificate.
type entityTypeCertificate struct {
	entityTypeCommon
}

func (e entityTypeCertificate) code() int64 {
	return entityTypeCodeCertificate
}

func (e entityTypeCertificate) allURLsQuery() string {
	return fmt.Sprintf(
		`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d AND type IN (%d, %d, %d, %d, %d)`,
		e.code(),
		authMethodTLS,
		identityTypeCertificateClientRestricted,
		identityTypeCertificateClientUnrestricted,
		identityTypeCertificateServer,
		identityTypeCertificateMetricsRestricted,
		identityTypeCertificateMetricsUnrestricted,
	)
}

func (e entityTypeCertificate) urlByIDQuery() string {
	return fmt.Sprintf(`%s AND identities.id = ?`, e.allURLsQuery())
}

func (e entityTypeCertificate) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND identities.identifier = ? 
	AND identities.auth_method = %d
	AND identities.type IN (%d, %d, %d, %d, %d)
`, authMethodTLS,
		identityTypeCertificateClientRestricted,
		identityTypeCertificateClientUnrestricted,
		identityTypeCertificateServer,
		identityTypeCertificateMetricsRestricted,
		identityTypeCertificateMetricsUnrestricted,
	)
}
