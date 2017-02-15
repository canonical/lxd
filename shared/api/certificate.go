package api

// CertificatesPost represents the fields of a new LXD certificate
type CertificatesPost struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Certificate string `json:"certificate" yaml:"certificate"`
	Password    string `json:"password" yaml:"password"`
}

// Certificate represents a LXD certificate
type Certificate struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Certificate string `json:"certificate" yaml:"certificate"`
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`
}
