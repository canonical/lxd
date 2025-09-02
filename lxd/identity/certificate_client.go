package identity

import (
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared/api"
)

// CertificateClient represents an identity that authenticates using TLS certificates
// and whose permissions are managed via group membership. It supports both caching
// and fine-grained permissions but is not an admin by default.
type CertificateClient struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that client certificates authenticate using TLS.
func (CertificateClient) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClient) Code() int64 {
	return identityTypeCertificateClient
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (CertificateClient) IsFineGrained() bool {
	return true
}

// Name returns the API name of this identity type.
func (CertificateClient) Name() string {
	return api.IdentityTypeCertificateClient
}

// CertificateClientPending represents an identity for which a token has been issued but who has not yet authenticated with LXD.
// It supports fine-grained permission management (e.g. the identity can be added to groups while in a pending state,
// allowing the token holder to assume the correct permissions when they eventually use the token to gain trust).
type CertificateClientPending struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that pending client certificates authenticate using TLS.
func (CertificateClientPending) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClientPending) Code() int64 {
	return identityTypeCertificateClientPending
}

// IsFineGrained indicates that this identity uses fine-grained permissions.
func (CertificateClientPending) IsFineGrained() bool {
	return true
}

// IsPending indicates that this identity is pending.
func (CertificateClientPending) IsPending() bool {
	return true
}

// Name returns the API name of this identity type.
func (CertificateClientPending) Name() string {
	return api.IdentityTypeCertificateClientPending
}

// CertificateClientRestricted represents an identity that authenticates using TLS certificates
// and is not privileged. It supports caching but does not support fine-grained permissions
// and is not an admin.
type CertificateClientRestricted struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that restricted client certificates authenticate using TLS.
func (CertificateClientRestricted) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClientRestricted) Code() int64 {
	return identityTypeCertificateClientRestricted
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
func (CertificateClientRestricted) LegacyCertificateType() (certificate.Type, error) {
	return certificate.TypeClient, nil
}

// Name returns the API name of this identity type.
func (CertificateClientRestricted) Name() string {
	return api.IdentityTypeCertificateClientRestricted
}

// CertificateClientUnrestricted represents an identity that authenticates using TLS certificates
// and is privileged with administrator access. It supports caching, has admin privileges,
// but does not support fine-grained permissions.
type CertificateClientUnrestricted struct {
	typeInfoCommon
}

// AuthenticationMethod indicates that unrestricted client certificates authenticate using TLS.
func (CertificateClientUnrestricted) AuthenticationMethod() string {
	return api.AuthenticationMethodTLS
}

// Code returns the identity type code for this identity type.
func (CertificateClientUnrestricted) Code() int64 {
	return identityTypeCertificateClientUnrestricted
}

// IsAdmin indicates that this identity type has administrator privileges (unrestricted).
func (CertificateClientUnrestricted) IsAdmin() bool {
	return true
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
func (CertificateClientUnrestricted) LegacyCertificateType() (certificate.Type, error) {
	return certificate.TypeClient, nil
}

// Name returns the API name of this identity type.
func (CertificateClientUnrestricted) Name() string {
	return api.IdentityTypeCertificateClientUnrestricted
}
