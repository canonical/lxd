package identity

import (
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared/api"
)

// CertificateServer represents cluster member authentication using TLS certificates.
// It has administrator privileges and supports caching but does not support fine-grained permissions.
type CertificateServer struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that server certificates authenticate using TLS.
func (CertificateServer) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateServer) Code() int64 {
	return identityTypeCertificateServer
}

// IsAdmin indicates that this identity type has administrator privileges (unrestricted).
func (CertificateServer) IsAdmin() bool {
	return true
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
func (CertificateServer) LegacyCertificateType() (certificate.Type, error) {
	return certificate.TypeServer, nil
}

// Name returns the API name of this identity type.
func (CertificateServer) Name() string {
	return api.IdentityTypeCertificateServer
}
