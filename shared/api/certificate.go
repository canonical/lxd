package api

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// CertificateTypeClient indicates a client certificate type.
const CertificateTypeClient = "client"

// CertificateTypeServer indicates a server certificate type.
const CertificateTypeServer = "server"

// CertificateTypeMetrics indicates a metrics certificate type.
const CertificateTypeMetrics = "metrics"

// CertificateTypeUnknown indicates an unknown certificate type.
const CertificateTypeUnknown = "unknown"

// CertificatesPost represents the fields of a new LXD certificate
//
// swagger:model
type CertificatesPost struct {
	// Name associated with the certificate
	// Example: castiana
	Name string `json:"name" yaml:"name"`

	// Usage type for the certificate
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

	// The certificate itself, as base64 encoded X509 PEM certificate
	// Example: base64 encoded X509 PEM certificate
	//
	// API extension: certificate_self_renewal
	Certificate string `json:"certificate" yaml:"certificate"`

	// Server trust password (used to add an untrusted client, deprecated, use trust_token)
	// Example: blah
	Password string `json:"password" yaml:"password"` // Deprecated, use TrustToken.

	// Trust token (used to add an untrusted client)
	// Example: blah
	//
	// API extension: explicit_trust_token
	TrustToken string `json:"trust_token" yaml:"trust_token"`

	// Whether to create a certificate add token
	// Example: true
	//
	// API extension: certificate_token
	Token bool `json:"token" yaml:"token"`
}

// CertificatePut represents the modifiable fields of a LXD certificate
//
// swagger:model
//
// API extension: certificate_update.
type CertificatePut struct {
	// Name associated with the certificate
	// Example: castiana
	Name string `json:"name" yaml:"name"`

	// Usage type for the certificate
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

	// The certificate itself, as PEM encoded X509 certificate
	// Example: X509 PEM certificate
	//
	// API extension: certificate_self_renewal
	Certificate string `json:"certificate" yaml:"certificate"`
}

// Certificate represents a LXD certificate
//
// swagger:model
type Certificate struct {
	// Name associated with the certificate
	// Example: castiana
	Name string `json:"name" yaml:"name"`

	// Usage type for the certificate
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

	// The certificate itself, as PEM encoded X509 certificate
	// Example: X509 PEM certificate
	//
	// API extension: certificate_self_renewal
	Certificate string `json:"certificate" yaml:"certificate"`

	// SHA256 fingerprint of the certificate
	// Read only: true
	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`
}

// Writable converts a full Certificate struct into a CertificatePut struct (filters read-only fields).
func (c *Certificate) Writable() CertificatePut {
	return CertificatePut{
		Name:        c.Name,
		Type:        c.Type,
		Restricted:  c.Restricted,
		Projects:    c.Projects,
		Certificate: c.Certificate,
	}
}

// SetWritable sets applicable values from CertificatePut struct to Certificate struct.
func (c *Certificate) SetWritable(put CertificatePut) {
	c.Name = put.Name
	c.Type = put.Type
	c.Restricted = put.Restricted
	c.Projects = put.Projects
	c.Certificate = put.Certificate
}

// URL returns the URL for the certificate.
func (c *Certificate) URL(apiVersion string) *URL {
	return NewURL().Path(apiVersion, "certificates", c.Fingerprint)
}

// CertificateAddToken represents the fields contained within an encoded certificate add token.
//
// swagger:model
//
// API extension: certificate_token.
type CertificateAddToken struct {
	// The name of the new client
	// Example: user@host
	ClientName string `json:"client_name" yaml:"client_name"`

	// The fingerprint of the network certificate
	// Example: 57bb0ff4340b5bb28517e062023101adf788c37846dc8b619eb2c3cb4ef29436
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`

	// The addresses of the server
	// Example: ["10.98.30.229:8443"]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// The random join secret
	// Example: 2b2284d44db32675923fe0d2020477e0e9be11801ff70c435e032b97028c35cd
	Secret string `json:"secret" yaml:"secret"`

	// The token's expiry date.
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// Identity is an indicator for which API (certificates or identities) to send the token.
	// Example: true
	//
	// API extension: identity_management
	Identity bool `json:"identity_type" yaml:"identity_type"`
}

// String encodes the certificate add token as JSON and then base64.
func (t *CertificateAddToken) String() string {
	joinTokenJSON, err := json.Marshal(t)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(joinTokenJSON)
}
