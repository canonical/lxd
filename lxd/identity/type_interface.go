package identity

import (
	"net/http"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared/api"
)

// Type represents an identity type in LXD.
// It defines the methods that all identity types must implement to provide
// authentication, authorization, and caching behavior.
//
// To add a new identity type:
// 1. Add a new identity type code const below.
// 2. Add a new struct that implements this interface.
// 3. Add new type to [types] slice.
// 4. Add an API type in shared/api/auth.go.
// 5. Implement db functions in db/cluster/identities.go (if needed).
type Type interface {
	// AuthenticationMethod returns the authentication method used by this identity type.
	AuthenticationMethod() string

	// Code returns the identity type code for this identity type.
	Code() int64

	// IsAdmin returns true if this identity type has administrator privileges (unrestricted).
	IsAdmin() bool

	// IsFineGrained returns true if this identity type supports fine-grained permissions (managed via group ownership).
	IsFineGrained() bool

	// IsPending returns true if this identity type is a pending variant.
	IsPending() bool

	// LegacyCertificateType returns the legacy certificate type for this identity type.
	// If an error is returned, it indicates that the identity type does not correspond to a legacy certificate type.
	LegacyCertificateType() (certificate.Type, error)

	// Name returns the API name of this identity type.
	Name() string
}

const (
	// identityTypeCertificateClientRestricted represents identities that authenticate using TLS and are not privileged.
	identityTypeCertificateClientRestricted int64 = 1

	// identityTypeCertificateClientUnrestricted represents identities that authenticate using TLS and are privileged.
	identityTypeCertificateClientUnrestricted int64 = 2

	// identityTypeCertificateServer represents cluster member authentication.
	identityTypeCertificateServer int64 = 3

	// identityTypeCertificateMetricsRestricted represents identities that may only view metrics and are not privileged.
	identityTypeCertificateMetricsRestricted int64 = 4

	// identityTypeOIDCClient represents an identity that authenticates with OIDC.
	identityTypeOIDCClient int64 = 5

	// identityTypeCertificateMetricsUnrestricted represents identities that may only view metrics and are privileged.
	identityTypeCertificateMetricsUnrestricted int64 = 6

	// identityTypeCertificateClient represents identities that authenticate using TLS and whose permissions are managed via group membership.
	identityTypeCertificateClient int64 = 7

	// identityTypeCertificateClientPending represents identities for which a token has been issued but who have not yet authenticated with LXD.
	identityTypeCertificateClientPending int64 = 8

	// identityTypeBearerDevLXD is the code for [api.IdentityTypeBearerTokenDevLXD].
	identityTypeBearerDevLXD int64 = 9
)

// types is a slice of all identity types that implement the [Type] interface.
var types = []Type{
	OIDCClient{},
	CertificateClient{},
	CertificateClientPending{},
	CertificateClientRestricted{},
	CertificateClientUnrestricted{},
	CertificateMetricsRestricted{},
	CertificateMetricsUnrestricted{},
	CertificateServer{},
	DevLXDTokenBearer{},
}

var nameToType = make(map[string]Type, len(types))
var codeToType = make(map[int64]Type, len(types))

// init initializes the [nameToType] and [codeToType] maps for quick lookup of identity types by name or code.
func init() {
	for _, t := range types {
		nameToType[t.Name()] = t
		codeToType[t.Code()] = t
	}
}

// New creates a new identity type based on the provided identity type string.
// It validates the identity type string and returns the appropriate identity type struct that implements the [Type] interface.
// It returns [http.StatusBadRequest] wrapped in [api.StatusErrorf] if the identity type is not recognized.
func New(name string) (Type, error) {
	t, ok := nameToType[name]
	if !ok {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Unrecognized identity type %q", name)
	}

	return t, nil
}

// NewFromCode creates a new identity type based on the provided identity type code.
// It validates the identity type code and returns the appropriate identity type struct that implements the [Type] interface.
// It returns [http.StatusInternalServerError] wrapped in [api.StatusErrorf] if the identity type is not recognized.
// Prefer [New] over this function when validating identity types from input, as [New] returns [http.StatusBadRequest] for unrecognized types. This function is used in the implementation of [query.IntegerScanner] for [IdentityType] when reading from the database.
func NewFromCode(code int64) (Type, error) {
	t, ok := codeToType[code]
	if !ok {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Unrecognized identity type code %d", code)
	}

	return t, nil
}

// Types returns a slice of all identity types that implement the [Type] interface.
// The returned slice must not be modified by callers.
func Types() []Type {
	return types
}
