//go:build linux && cgo && !agent

package cluster

import (
	"time"
)

// CertificatesRow represents a row of the certificates table.
// db:model certificates
type CertificatesRow struct {
	ID          int64  `db:"id"`
	Fingerprint string `db:"fingerprint"`
	Certificate string `db:"certificate"`

	// CreationDate has a default value of the current timestamp.
	// We want the database to set this, and we never want to update it.
	// Omit the fields from create and update statements/values via the omit directive.
	//
	// db:omit create update
	CreationDate time.Time `db:"creation_date"`
}

// APIName implements [query.APINamer] for clear API error messages.
func (CertificatesRow) APIName() string {
	return "Certificate"
}

// IdentityCertificate represents a certificate that is associated with an [IdentitiesRow].
// db:model certificates
type IdentityCertificate struct {
	Row CertificatesRow

	// db:join JOIN identities_certificates ON certificates.id = identities_certificates.certificate_id
	IdentityID int64 `db:"identities_certificates.identity_id"`
}
