//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// CertificateGenerated is an interface of generated methods for Certificate
type CertificateGenerated interface {
	// GetCertificates returns all available certificates.
	// generator: certificate GetMany
	GetCertificates(filter CertificateFilter) ([]Certificate, error)

	// GetCertificate returns the certificate with the given key.
	// generator: certificate GetOne
	GetCertificate(fingerprint string) (*Certificate, error)

	// GetCertificateID return the ID of the certificate with the given key.
	// generator: certificate ID
	GetCertificateID(fingerprint string) (int64, error)

	// CertificateExists checks if a certificate with the given key exists.
	// generator: certificate Exists
	CertificateExists(fingerprint string) (bool, error)

	// CreateCertificate adds a new certificate to the database.
	// generator: certificate Create
	CreateCertificate(object Certificate) (int64, error)

	// DeleteCertificate deletes the certificate matching the given key parameters.
	// generator: certificate DeleteOne-by-Fingerprint
	DeleteCertificate(fingerprint string) error

	// DeleteCertificates deletes the certificate matching the given key parameters.
	// generator: certificate DeleteMany-by-Name-and-Type
	DeleteCertificates(name string, certificateType CertificateType) error

	// UpdateCertificate updates the certificate matching the given key parameters.
	// generator: certificate Update
	UpdateCertificate(fingerprint string, object Certificate) error
}
