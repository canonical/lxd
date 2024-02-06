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

// DeleteCertificateProjects deletes the certificate_project matching the given key parameters.
func DeleteCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int) error {
	return DeleteIdentityProjects(ctx, tx, certificateID)
}

// CreateCertificateProjects adds a new certificate_project to the database.
func CreateCertificateProjects(ctx context.Context, tx *sql.Tx, objects []CertificateProject) error {
	identityProjects := make([]IdentityProject, 0, len(objects))
	for _, certificateProject := range objects {
		identityProjects = append(identityProjects, IdentityProject{
			IdentityID: certificateProject.CertificateID,
			ProjectID:  certificateProject.ProjectID,
		})
	}

	return CreateIdentityProjects(ctx, tx, identityProjects)
}

// UpdateCertificateProjects updates the certificate_project matching the given key parameters.
func UpdateCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int, projectNames []string) error {
	return UpdateIdentityProjects(ctx, tx, certificateID, projectNames)
}
