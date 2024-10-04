//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// CertificateProject is an association table struct that associates
// Certificates to Projects.
type CertificateProject struct {
	CertificateID int `db:"primary=yes"`
	ProjectID     int
}

// CertificateProjectFilter specifies potential query parameter fields.
type CertificateProjectFilter struct {
	CertificateID *int
	ProjectID     *int
}

// GetCertificateProjects returns all available Projects for the Certificate.
func GetCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int) ([]Project, error) {
	return GetIdentityProjects(ctx, tx, certificateID)
}

// UpdateCertificateProjects updates the certificate_project matching the given key parameters.
func UpdateCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int, projectNames []string) error {
	return UpdateIdentityProjects(ctx, tx, certificateID, projectNames)
}
