package identity

import (
	"github.com/canonical/lxd/shared/api"
)

// CertificateClientClusterLink represents a cluster link that authenticates using TLS certificates
// and whose permissions are managed via group membership. It supports both caching
// and fine-grained permissions but is not an admin by default.
type CertificateClientClusterLink struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that cluster links authenticate using TLS.
func (CertificateClientClusterLink) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClientClusterLink) Code() int64 {
	return identityTypeCertificateClusterLink
}

// IsCacheable indicates that this identity can be cached.
func (CertificateClientClusterLink) IsCacheable() bool {
	return true
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (CertificateClientClusterLink) IsFineGrained() bool {
	return true
}

// Name returns the API name of this identity type.
func (CertificateClientClusterLink) Name() string {
	return api.IdentityTypeCertificateClusterLink
}

// CertificateClientClusterLinkPending represents a cluster link for which a token has been issued
// but who has not yet been activated by a linked cluster. It supports fine-grained permissions
// but is not cacheable and not an admin.
type CertificateClientClusterLinkPending struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that pending cluster links authenticate using TLS.
func (CertificateClientClusterLinkPending) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClientClusterLinkPending) Code() int64 {
	return identityTypeCertificateClusterLinkPending
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (CertificateClientClusterLinkPending) IsFineGrained() bool {
	return true
}

// IsPending indicates that this identity is pending.
func (CertificateClientClusterLinkPending) IsPending() bool {
	return true
}

// Name returns the API name of this identity type.
func (CertificateClientClusterLinkPending) Name() string {
	return api.IdentityTypeCertificateClusterLinkPending
}
