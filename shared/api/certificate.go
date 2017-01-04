package api

// CertificatesPost represents the fields of a new LXD certificate
type CertificatesPost struct {
	CertificatePut `yaml:",inline"`

	Certificate string `json:"certificate"`
	Password    string `json:"password"`
}

// CertificatePut represents the modifiable fields of a LXD certificate
//
// API extension: certificate_update
type CertificatePut struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Certificate represents a LXD certificate
type Certificate struct {
	CertificatePut `yaml:",inline"`

	Certificate string `json:"certificate"`
	Fingerprint string `json:"fingerprint"`
}

// Writable converts a full Certificate struct into a CertificatePut struct (filters read-only fields)
func (cert *Certificate) Writable() CertificatePut {
	return cert.CertificatePut
}
