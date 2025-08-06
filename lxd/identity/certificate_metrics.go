package identity

import (
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

// IsCacheable indicates that this identity can be cached.
func (CertificateMetricsRestricted) IsCacheable() bool {
	return true
}

// CertificateMetricsUnrestricted represents an identity that can view metrics and is privileged.
type CertificateMetricsUnrestricted struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that unrestricted metrics certificates authenticate using TLS.
func (CertificateMetricsUnrestricted) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// IsCacheable indicates that this identity can be cached.
func (CertificateMetricsUnrestricted) IsCacheable() bool {
	return true
}
