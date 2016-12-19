package api

// CertificatesPost represents the fields of a new LXD certificate
type CertificatesPost struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Certificate string `json:"certificate"`
	Password    string `json:"password"`
}

// Certificate represents a LXD certificate
type Certificate struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Certificate string `json:"certificate"`
	Fingerprint string `json:"fingerprint"`
}
