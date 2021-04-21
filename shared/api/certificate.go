package api

// CertificateTypeClient indicates a client certificate type.
const CertificateTypeClient = "client"

// CertificateTypeServer indicates a server certificate type.
const CertificateTypeServer = "server"

// CertificateTypeUnknown indicates an unknown certificate type.
const CertificateTypeUnknown = "unknown"

// CertificatesPost represents the fields of a new LXD certificate
//
// swagger:model
type CertificatesPost struct {
	CertificatePut `yaml:",inline"`

	// The certificate itself, as PEM encoded X509
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Server trust password (used to add an untrusted client)
	// Example: blah
	Password string `json:"password" yaml:"password"`
}

// CertificatePut represents the modifiable fields of a LXD certificate
//
// swagger:model
//
// API extension: certificate_update
type CertificatePut struct {
	// Name associated with the certificate
	// Example: castiana
	Name string `json:"name" yaml:"name"`

	// Usage type for the certificate (only client currently)
	// Example: client
	Type string `json:"type" yaml:"type"`

	// Whether to limit the certificate to listed projects
	// Example: true
	//
	// API extension: certificate_project
	Restricted bool `json:"restricted" yaml:"restricted"`

	// List of allowed projects (applies when restricted)
	// Example: ["default", "foo", "bar"]
	//
	// API extension: certificate_project
	Projects []string `json:"projects" yaml:"projects"`
}

// Certificate represents a LXD certificate
//
// swagger:model
type Certificate struct {
	CertificatePut `yaml:",inline"`

	// The certificate itself, as PEM encoded X509
	// Read only: true
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// SHA256 fingerprint of the certificate
	// Read only: true
	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`
}

// Writable converts a full Certificate struct into a CertificatePut struct (filters read-only fields)
func (cert *Certificate) Writable() CertificatePut {
	return cert.CertificatePut
}
