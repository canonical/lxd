package api

// CertificatesPost represents the fields of a new LXD certificate
//
// swagger:model
type CertificatesPost struct {
	CertificatePut `yaml:",inline"`

	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Example: blah
	Password string `json:"password" yaml:"password"`
}

// CertificatePut represents the modifiable fields of a LXD certificate
//
// API extension: certificate_update
//
// swagger:model
type CertificatePut struct {
	// Example: castiana
	Name string `json:"name" yaml:"name"`

	// Example: client
	Type string `json:"type" yaml:"type"`

	// API extension: certificate_project
	//
	// Example: true
	Restricted bool `json:"restricted" yaml:"restricted"`

	// API extension: certificate_project
	//
	// Example: ["default", "foo", "bar"]
	Projects []string `json:"projects" yaml:"projects"`
}

// Certificate represents a LXD certificate
//
// swagger:model
type Certificate struct {
	CertificatePut `yaml:",inline"`

	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`
}

// Writable converts a full Certificate struct into a CertificatePut struct (filters read-only fields)
func (cert *Certificate) Writable() CertificatePut {
	return cert.CertificatePut
}
