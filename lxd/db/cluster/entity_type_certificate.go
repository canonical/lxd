package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeCertificate implements entityType for a Certificate.
type entityTypeCertificate struct {
	entity.Certificate
}

// Code returns entityTypeCodeCertificate.
func (e entityTypeCertificate) Code() int64 {
	return entityTypeCodeCertificate
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeCertificate, the ID of the Certificate,
// the project name of the Certificate, the location of the Certificate, and the path arguments of the
// Certificate in the order that they are found in its URL.
func (e entityTypeCertificate) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d`, e.Code(), authMethodTLS)
}

// URLsByProjectQuery returns an empty string because Certificate entities are not project specific.
func (e entityTypeCertificate) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeCertificate) URLByIDQuery() string {
	return fmt.Sprintf(`%s AND identities.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeCertificate) IDFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND identities.identifier = ? 
	AND identities.auth_method = %d
`, authMethodTLS)
}

// OnDeleteTriggerName returns an empty string because there is no `certificates` table (these are now in the `identities` table).
func (e entityTypeCertificate) OnDeleteTriggerName() string {
	return ""
}

// OnDeleteTriggerSQL  returns an empty string because there is no `certificates` table (these are now in the `identities` table).
func (e entityTypeCertificate) OnDeleteTriggerSQL() string {
	return ""
}
