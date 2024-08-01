package cluster

import (
	"fmt"
)

// entityTypeCertificate implements entityTypeDBInfo for a Certificate.
type entityTypeCertificate struct{}

func (e entityTypeCertificate) code() int64 {
	return entityTypeCodeCertificate
}

func (e entityTypeCertificate) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d`, e.code(), authMethodTLS)
}

func (e entityTypeCertificate) urlsByProjectQuery() string {
	return ""
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
`, authMethodTLS)
}

func (e entityTypeCertificate) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}
