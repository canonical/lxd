//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// GetCertificateProjects returns all available Projects for the Certificate.
func GetCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int64) ([]Project, error) {
	return GetIdentityProjects(ctx, tx, certificateID)
}

// UpdateCertificateProjects updates the certificate_project matching the given key parameters.
func UpdateCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int, projectNames []string) error {
	return UpdateIdentityProjects(ctx, tx, certificateID, projectNames)
}
