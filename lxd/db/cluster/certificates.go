//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
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

// GetIdentitiesPEMCertificates returns a map of identity ID to list of PEM encoded certificates for each identity.
// The optional identity ID field can be used to filter the output (in which case the output map will contain only one key).
// The slice of certificates for each identity is ordered with the most recent certificate first.
func GetIdentitiesPEMCertificates(ctx context.Context, tx *sql.Tx, identityID *int64) (map[int64][]string, error) {
	var b strings.Builder
	var args []any
	if identityID != nil {
		args = []any{*identityID}
		b.WriteString("WHERE identities_certificates.identity_id = ? ")
	}

	// Sorting by certificates.id DESC as this will be more efficient than sorting by creation date.
	// Since id is the auto-incrementing primary key, it has the same effect.
	b.WriteString("ORDER BY identities_certificates.identity_id, certificates.id DESC")
	out := make(map[int64][]string)
	err := query.SelectFunc[IdentityCertificate](ctx, tx, b.String(), func(t IdentityCertificate) error {
		out[t.IdentityID] = append(out[t.IdentityID], t.Row.Certificate)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return out, nil
}
