package identity

import (
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared/api"
)

// CertificateMetricsRestricted represents an identity that can view metrics and is not privileged.
type CertificateMetricsRestricted struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that restricted metrics certificates authenticate using TLS.
func (CertificateMetricsRestricted) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateMetricsRestricted) Code() int64 {
	return identityTypeCertificateMetricsRestricted
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
func (CertificateMetricsRestricted) LegacyCertificateType() (certificate.Type, error) {
	return certificate.TypeMetrics, nil
}

// Name returns the API name of this identity type.
func (CertificateMetricsRestricted) Name() string {
	return api.IdentityTypeCertificateMetricsRestricted
}

// CertificateMetricsUnrestricted represents an identity that can view metrics and is privileged.
type CertificateMetricsUnrestricted struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that unrestricted metrics certificates authenticate using TLS.
func (CertificateMetricsUnrestricted) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateMetricsUnrestricted) Code() int64 {
	return identityTypeCertificateMetricsUnrestricted
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
func (CertificateMetricsUnrestricted) LegacyCertificateType() (certificate.Type, error) {
	return certificate.TypeMetrics, nil
}

// Name returns the API name of this identity type.
func (CertificateMetricsUnrestricted) Name() string {
	return api.IdentityTypeCertificateMetricsUnrestricted
}
