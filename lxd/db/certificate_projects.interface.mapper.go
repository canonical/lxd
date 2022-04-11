//go:build linux && cgo && !agent

package db

// CertificateProjectGenerated is an interface of generated methods for CertificateProject
type CertificateProjectGenerated interface {
	// GetCertificateProjects returns all available Projects for the Certificate.
	// generator: certificate_project GetMany
	GetCertificateProjects(certificateID int) ([]Project, error)

	// DeleteCertificateProjects deletes the certificate_project matching the given key parameters.
	// generator: certificate_project DeleteMany
	DeleteCertificateProjects(certificateID int) error

	// CreateCertificateProject adds a new certificate_project to the database.
	// generator: certificate_project Create
	CreateCertificateProject(object CertificateProject) (int64, error)

	// UpdateCertificateProjects updates the certificate_project matching the given key parameters.
	// generator: certificate_project Update
	UpdateCertificateProjects(certificateID int, projectNames []string) error
}
