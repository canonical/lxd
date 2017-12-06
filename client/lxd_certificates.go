package lxd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// Certificate handling functions

// GetCertificateFingerprints returns a list of certificate fingerprints
func (r *ProtocolLXD) GetCertificateFingerprints() ([]string, error) {
	certificates := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/certificates", nil, "", &certificates)
	if err != nil {
		return nil, err
	}

	// Parse it
	fingerprints := []string{}
	for _, fingerprint := range fingerprints {
		fields := strings.Split(fingerprint, "/certificates/")
		fingerprints = append(fingerprints, fields[len(fields)-1])
	}

	return fingerprints, nil
}

// GetCertificates returns a list of certificates
func (r *ProtocolLXD) GetCertificates() ([]api.Certificate, error) {
	certificates := []api.Certificate{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/certificates?recursion=1", nil, "", &certificates)
	if err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificate returns the certificate entry for the provided fingerprint
func (r *ProtocolLXD) GetCertificate(fingerprint string) (*api.Certificate, string, error) {
	certificate := api.Certificate{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/certificates/%s", url.QueryEscape(fingerprint)), nil, "", &certificate)
	if err != nil {
		return nil, "", err
	}

	return &certificate, etag, nil
}

// CreateCertificate adds a new certificate to the LXD trust store
func (r *ProtocolLXD) CreateCertificate(certificate api.CertificatesPost) error {
	// Send the request
	_, _, err := r.query("POST", "/certificates", certificate, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateCertificate updates the certificate definition
func (r *ProtocolLXD) UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) error {
	if !r.HasExtension("certificate_update") {
		return fmt.Errorf("The server is missing the required \"certificate_update\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/certificates/%s", url.QueryEscape(fingerprint)), certificate, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteCertificate removes a certificate from the LXD trust store
func (r *ProtocolLXD) DeleteCertificate(fingerprint string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/certificates/%s", url.QueryEscape(fingerprint)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
