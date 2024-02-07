//go:build linux && cgo && !agent

package cluster

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/auth"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t identities.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e identity objects table=identities
//go:generate mapper stmt -e identity objects-by-ID table=identities
//go:generate mapper stmt -e identity objects-by-AuthMethod table=identities
//go:generate mapper stmt -e identity objects-by-AuthMethod-and-Type table=identities
//go:generate mapper stmt -e identity objects-by-AuthMethod-and-Identifier table=identities
//go:generate mapper stmt -e identity objects-by-AuthMethod-and-Name table=identities
//go:generate mapper stmt -e identity objects-by-Type table=identities
//go:generate mapper stmt -e identity id table=identities
//go:generate mapper stmt -e identity create struct=Identity table=identities
//go:generate mapper stmt -e identity delete-by-AuthMethod-and-Identifier table=identities
//go:generate mapper stmt -e identity delete-by-Name-and-Type table=identities
//go:generate mapper stmt -e identity update struct=Identity table=identities
//
//go:generate mapper method -i -e identity GetMany
//go:generate mapper method -i -e identity GetOne
//go:generate mapper method -i -e identity ID struct=Identity
//go:generate mapper method -i -e identity Exists struct=Identity
//go:generate mapper method -i -e identity Create struct=Identity
//go:generate mapper method -i -e identity DeleteOne-by-AuthMethod-and-Identifier
//go:generate mapper method -i -e identity DeleteMany-by-Name-and-Type
//go:generate mapper method -i -e identity Update struct=Identity

// AuthMethod is a database representation of an authentication method.
//
// AuthMethod is defined on string so that API constants can be converted by casting. The sql.Scanner and
// driver.Valuer interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an AuthMethod as they are read from the
// database. It is not possible to read/write an invalid authentication methods from/to the database when using this type.
type AuthMethod string

const (
	authMethodTLS  int64 = 1
	authMethodOIDC int64 = 2
)

// Scan implements sql.Scanner for AuthMethod. This converts the integer value back into the correct API constant or
// returns an error.
func (a *AuthMethod) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("Authentication method cannot be null")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid authentication method type: %w", err)
	}

	authMethodInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Authentication method should be an integer, got `%v` (%T)", intValue, intValue)
	}

	switch authMethodInt {
	case authMethodTLS:
		*a = api.AuthenticationMethodTLS
	case authMethodOIDC:
		*a = api.AuthenticationMethodOIDC
	default:
		return fmt.Errorf("Unknown authentication method `%d`", authMethodInt)
	}

	return nil
}

// Value implements driver.Valuer for AuthMethod. This converts the API constant into an integer or throws an error.
func (a AuthMethod) Value() (driver.Value, error) {
	switch a {
	case api.AuthenticationMethodTLS:
		return authMethodTLS, nil
	case api.AuthenticationMethodOIDC:
		return authMethodOIDC, nil
	}

	return nil, fmt.Errorf("Invalid authentication method %q", a)
}

// IdentityType indicates the type of the identity.
//
// IdentityType is defined on string so that API constants can be converted by casting. The sql.Scanner and
// driver.Valuer interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an IdentityType as they are read from the
// database. It is not possible to read/write an invalid identity types from/to the database when using this type.
type IdentityType string

const (
	identityTypeCertificateClientRestricted   int64 = 1
	identityTypeCertificateClientUnrestricted int64 = 2
	identityTypeCertificateServer             int64 = 3
	identityTypeCertificateMetrics            int64 = 4
)

// Scan implements sql.Scanner for IdentityType. This converts the integer value back into the correct API constant or
// returns an error.
func (i *IdentityType) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("Identity type cannot be null")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid identity type: %w", err)
	}

	identityTypeInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Identity type should be an integer, got `%v` (%T)", intValue, intValue)
	}

	switch identityTypeInt {
	case identityTypeCertificateClientRestricted:
		*i = api.IdentityTypeCertificateClientRestricted
	case identityTypeCertificateClientUnrestricted:
		*i = api.IdentityTypeCertificateClientUnrestricted
	case identityTypeCertificateServer:
		*i = api.IdentityTypeCertificateServer
	case identityTypeCertificateMetrics:
		*i = api.IdentityTypeCertificateMetrics
	default:
		return fmt.Errorf("Unknown identity type `%d`", identityTypeInt)
	}

	return nil
}

// Value implements driver.Valuer for IdentityType. This converts the API constant into an integer or throws an error.
func (i IdentityType) Value() (driver.Value, error) {
	switch i {
	case api.IdentityTypeCertificateClientRestricted:
		return identityTypeCertificateClientRestricted, nil
	case api.IdentityTypeCertificateClientUnrestricted:
		return identityTypeCertificateClientUnrestricted, nil
	case api.IdentityTypeCertificateServer:
		return identityTypeCertificateServer, nil
	case api.IdentityTypeCertificateMetrics:
		return identityTypeCertificateMetrics, nil
	}

	return nil, fmt.Errorf("Invalid identity type %q", i)
}

// toCertificateAPIType returns the API equivalent type.
func (i IdentityType) toCertificateType() (certificate.Type, error) {
	switch i {
	case api.IdentityTypeCertificateClientRestricted:
		return certificate.TypeClient, nil
	case api.IdentityTypeCertificateClientUnrestricted:
		return certificate.TypeClient, nil
	case api.IdentityTypeCertificateServer:
		return certificate.TypeServer, nil
	case api.IdentityTypeCertificateMetrics:
		return certificate.TypeMetrics, nil
	}

	return -1, fmt.Errorf("Identity type %q is not a certificate", i)
}

// Identity is a database representation of any authenticated party.
type Identity struct {
	ID         int
	AuthMethod AuthMethod `db:"primary=yes"`
	Type       IdentityType
	Identifier string `db:"primary=yes"`
	Name       string
	Metadata   string
}

// IdentityFilter contains fields upon which identities can be filtered.
type IdentityFilter struct {
	ID         *int
	AuthMethod *AuthMethod
	Type       *IdentityType
	Identifier *string
	Name       *string
}

// CertificateMetadata contains metadate for certificate identities. Currently this is only the certificate itself.
type CertificateMetadata struct {
	Certificate string `json:"cert"`
}

// ToCertificate converts an Identity to a Certificate.
func (i Identity) ToCertificate() (*Certificate, error) {
	identityType, err := i.Type.toCertificateType()
	if err != nil {
		return nil, fmt.Errorf("Failed converting identity type to certificate type: %w", err)
	}

	var metadata CertificateMetadata
	err = json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal certificate identity metadata: %w", err)
	}

	isRestricted, err := auth.IsRestrictedIdentityType(string(i.Type))
	if err != nil {
		return nil, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	c := &Certificate{
		ID:          i.ID,
		Fingerprint: i.Identifier,
		Type:        identityType,
		Name:        i.Name,
		Certificate: metadata.Certificate,
		Restricted:  isRestricted,
	}

	return c, nil
}
