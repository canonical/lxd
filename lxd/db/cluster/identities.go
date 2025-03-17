//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
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
//go:generate goimports -w identities.mapper.go
//go:generate goimports -w identities.interface.mapper.go

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
	identityTypeCertificateClientRestricted    int64 = 1
	identityTypeCertificateClientUnrestricted  int64 = 2
	identityTypeCertificateServer              int64 = 3
	identityTypeCertificateMetricsRestricted   int64 = 4
	identityTypeOIDCClient                     int64 = 5
	identityTypeCertificateMetricsUnrestricted int64 = 6
	identityTypeCertificateClient              int64 = 7
	identityTypeCertificateClientPending       int64 = 8
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
	case identityTypeCertificateMetricsRestricted:
		*i = api.IdentityTypeCertificateMetricsRestricted
	case identityTypeCertificateMetricsUnrestricted:
		*i = api.IdentityTypeCertificateMetricsUnrestricted
	case identityTypeOIDCClient:
		*i = api.IdentityTypeOIDCClient
	case identityTypeCertificateClient:
		*i = api.IdentityTypeCertificateClient
	case identityTypeCertificateClientPending:
		*i = api.IdentityTypeCertificateClientPending
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
	case api.IdentityTypeCertificateMetricsRestricted:
		return identityTypeCertificateMetricsRestricted, nil
	case api.IdentityTypeCertificateMetricsUnrestricted:
		return identityTypeCertificateMetricsUnrestricted, nil
	case api.IdentityTypeOIDCClient:
		return identityTypeOIDCClient, nil
	case api.IdentityTypeCertificateClient:
		return identityTypeCertificateClient, nil
	case api.IdentityTypeCertificateClientPending:
		return identityTypeCertificateClientPending, nil
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
	case api.IdentityTypeCertificateMetricsRestricted:
		return certificate.TypeMetrics, nil
	case api.IdentityTypeCertificateMetricsUnrestricted:
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

// CertificateMetadata contains metadata for certificate identities. Currently this is only the certificate itself.
type CertificateMetadata struct {
	Certificate string `json:"cert"`
}

// X509 returns an x509.Certificate from the CertificateMetadata.
func (c CertificateMetadata) X509() (*x509.Certificate, error) {
	certBlock, _ := pem.Decode([]byte(c.Certificate))
	if certBlock == nil {
		return nil, errors.New("Failed decoding certificate")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing certificate: %w", err)
	}

	return cert, nil
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

	isRestricted, err := identity.IsRestrictedIdentityType(string(i.Type))
	if err != nil {
		return nil, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	// Metrics certificates can be both restricted and unrestricted.
	// But an unrestricted metrics certificate has still less permissions as an unrestricted client certificate.
	// So it does not have full access to LXD only the metrics endpoint.
	if i.Type == api.IdentityTypeCertificateMetricsUnrestricted {
		isRestricted = false
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

// CertificateMetadata returns the metadata associated with the identity as CertificateMetadata. It fails if the
// authentication method is not api.AuthentictionMethodTLS or if the type is api.IdentityTypeClientCertificatePending,
// as they do not have metadata of this type.
func (i Identity) CertificateMetadata() (*CertificateMetadata, error) {
	if i.AuthMethod != api.AuthenticationMethodTLS {
		return nil, fmt.Errorf("Cannot get certificate metadata: Identity has authentication method %q (%q required)", i.AuthMethod, api.AuthenticationMethodTLS)
	}

	if i.Type == api.IdentityTypeCertificateClientPending {
		return nil, fmt.Errorf("Cannot get certificate metadata: Identity is pending")
	}

	var metadata CertificateMetadata
	err := json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal certificate identity metadata: %w", err)
	}

	return &metadata, nil
}

// X509 returns an x509.Certificate from the identity metadata. The AuthMethod of the Identity must be api.AuthenticationMethodTLS.
func (i Identity) X509() (*x509.Certificate, error) {
	metadata, err := i.CertificateMetadata()
	if err != nil {
		return nil, err
	}

	return metadata.X509()
}

// OIDCMetadata contains metadata for OIDC identities.
type OIDCMetadata struct {
	Subject           string   `json:"subject"`
	SessionID         string   `json:"session_id"`
	RefreshToken      string   `json:"refresh_token"`
	IDPGroups         []string `json:"idp_groups"`
	SessionTerminated bool     `json:"session_terminated"`
}

// OIDCMetadata returns the OIDC identity metadata.
func (i Identity) OIDCMetadata() (*OIDCMetadata, error) {
	if i.Type != api.IdentityTypeOIDCClient {
		return nil, api.NewStatusError(http.StatusBadRequest, "Cannot extract OIDC metadata: Identity is not an OIDC client")
	}

	var metadata OIDCMetadata
	err := json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed to unmarshal OIDC identity metadata: %w", err)
	}

	return &metadata, nil
}

// Subject returns OIDC subject from the identity metadata. The AuthMethod of the Identity must be api.AuthenticationMethodOIDC.
func (i Identity) Subject() (string, error) {
	if i.AuthMethod != api.AuthenticationMethodOIDC {
		return "", fmt.Errorf("Cannot extract subject from identity: Identity has authentication method %q (%q required)", i.AuthMethod, api.AuthenticationMethodOIDC)
	}

	var metadata OIDCMetadata
	err := json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return "", fmt.Errorf("Failed to unmarshal subject metadata: %w", err)
	}

	return metadata.Subject, nil
}

// PendingTLSMetadata contains metadata for the pending TLS certificate identity type.
type PendingTLSMetadata struct {
	Secret string    `json:"secret"`
	Expiry time.Time `json:"expiry"`
}

// PendingTLSMetadata returns the pending TLS identity metadata.
func (i Identity) PendingTLSMetadata() (*PendingTLSMetadata, error) {
	if i.Type != api.IdentityTypeCertificateClientPending {
		return nil, api.NewStatusError(http.StatusBadRequest, "Cannot extract pending TLS identity secret: Identity is not pending")
	}

	var metadata PendingTLSMetadata
	err := json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed to unmarshal pending TLS identity metadata: %w", err)
	}

	return &metadata, nil
}

// ToAPI converts an Identity to an api.Identity, executing database queries as necessary.
func (i *Identity) ToAPI(ctx context.Context, tx *sql.Tx, canViewGroup auth.PermissionChecker) (*api.Identity, error) {
	var tlsCertificate string
	if i.AuthMethod == api.AuthenticationMethodTLS && i.Type != api.IdentityTypeCertificateClientPending {
		metadata, err := i.CertificateMetadata()
		if err != nil {
			return nil, err
		}

		tlsCertificate = metadata.Certificate
	}

	// Shortcut group checks if identity is not fine-grained.
	if !identity.IsFineGrainedIdentityType(string(i.Type)) {
		return &api.Identity{
			AuthenticationMethod: string(i.AuthMethod),
			Type:                 string(i.Type),
			Identifier:           i.Identifier,
			Name:                 i.Name,
			TLSCertificate:       tlsCertificate,
		}, nil
	}

	groups, err := GetAuthGroupsByIdentityID(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	groupNames := make([]string, 0, len(groups))
	for _, group := range groups {
		if canViewGroup == nil || canViewGroup(entity.AuthGroupURL(group.Name)) {
			groupNames = append(groupNames, group.Name)
		}
	}

	return &api.Identity{
		AuthenticationMethod: string(i.AuthMethod),
		Type:                 string(i.Type),
		Identifier:           i.Identifier,
		Name:                 i.Name,
		Groups:               groupNames,
		TLSCertificate:       tlsCertificate,
	}, nil
}

// ToAPIInfo returns an api.IdentityInfo from the Identity. It shortcuts if the Identity is not fine-grained. For OIDC identities,
// it additionally returns a boolean indicating if the session was terminated and a refresh token.
func (i *Identity) ToAPIInfo(ctx context.Context, tx *sql.Tx, canViewGroup auth.PermissionChecker) (info *api.IdentityInfo, sessionTerminated bool, refreshToken string, err error) {
	apiIdentity, err := i.ToAPI(ctx, tx, canViewGroup)
	if err != nil {
		return nil, false, "", fmt.Errorf("Failed to populate LXD groups: %w", err)
	}

	// Shortcut further checks if identity is not fine-grained.
	if !identity.IsFineGrainedIdentityType(string(i.Type)) {
		return &api.IdentityInfo{
			Identity: *apiIdentity,
		}, false, "", nil
	}

	var idpGroupNames []string
	if apiIdentity.Type == api.IdentityTypeOIDCClient {
		oidcMetadata, err := i.OIDCMetadata()
		if err != nil {
			return nil, true, "", fmt.Errorf("Failed getting OIDC client metadata: %w", err)
		}

		sessionTerminated = oidcMetadata.SessionTerminated
		refreshToken = oidcMetadata.RefreshToken
		idpGroupNames = oidcMetadata.IDPGroups
	}

	effectiveGroups := apiIdentity.Groups
	mappedGroups, err := GetDistinctAuthGroupNamesFromIDPGroupNames(ctx, tx, idpGroupNames)
	if err != nil {
		return nil, false, "", fmt.Errorf("Failed to get effective groups: %w", err)
	}

	for _, mappedGroup := range mappedGroups {
		if !shared.ValueInSlice(mappedGroup, effectiveGroups) {
			effectiveGroups = append(effectiveGroups, mappedGroup)
		}
	}

	return &api.IdentityInfo{
		Identity:        *apiIdentity,
		EffectiveGroups: effectiveGroups,
		FineGrained:     identity.IsFineGrainedIdentityType(apiIdentity.Type),
	}, sessionTerminated, refreshToken, nil
}

// PopulateEffectivePermissions accepts an *api.IdentityInfo and adds the effective permissions based on the effective
// groups. This is done separately because an *api.IdentityInfo is already added to the request context after authentcation
// (sans effective permissions).
func PopulateEffectivePermissions(ctx context.Context, tx *sql.Tx, info *api.IdentityInfo) error {
	permissions, err := GetDistinctPermissionsByGroupNames(ctx, tx, info.EffectiveGroups)
	if err != nil {
		return fmt.Errorf("Failed to get effective permissions: %w", err)
	}

	permissions, entityURLs, err := GetPermissionEntityURLs(ctx, tx, permissions)
	if err != nil {
		return fmt.Errorf("Failed to get entity URLs for effective permissions: %w", err)
	}

	effectivePermissions := make([]api.Permission, 0, len(permissions))
	for _, permission := range permissions {
		effectivePermissions = append(effectivePermissions, api.Permission{
			EntityType:      string(permission.EntityType),
			EntityReference: entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String(),
			Entitlement:     string(permission.Entitlement),
		})
	}

	info.EffectivePermissions = effectivePermissions
	return nil
}

// ActivateTLSIdentity updates a TLS identity to make it valid by adding the fingerprint, PEM encoded certificate, and setting
// the type to api.IdentityTypeCertificateClient.
func ActivateTLSIdentity(ctx context.Context, tx *sql.Tx, identifier uuid.UUID, cert *x509.Certificate) error {
	fingerprint := shared.CertFingerprint(cert)
	_, err := GetIdentityID(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err == nil {
		return api.StatusErrorf(http.StatusConflict, "Identity already exists")
	}

	metadata := CertificateMetadata{Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))}
	b, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("Failed to encode certificate metadata: %w", err)
	}

	stmt := `UPDATE identities SET type = ?, identifier = ?, metadata = ? WHERE identifier = ? AND auth_method = ?`
	res, err := tx.ExecContext(ctx, stmt, identityTypeCertificateClient, fingerprint, string(b), identifier.String(), authMethodTLS)
	if err != nil {
		return fmt.Errorf("Failed to activate TLS identity: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to check for activated TLS identity: %w", err)
	}

	if n == 0 {
		return api.StatusErrorf(http.StatusNotFound, "No pending TLS identity found with identifier %q", identifier)
	} else if n > 1 {
		return fmt.Errorf("Unknown error occurred when activating a TLS identity: %w", err)
	}

	return nil
}

var getPendingTLSIdentityByTokenSecretStmt = fmt.Sprintf(`
SELECT identities.id, identities.auth_method, identities.type, identities.identifier, identities.name, identities.metadata
	FROM identities
	WHERE identities.type = %d
	AND json_extract(identities.metadata, '$.secret') = ?
`, identityTypeCertificateClientPending)

// GetPendingTLSIdentityByTokenSecret gets a single identity of type identityTypeCertificateClientPending with the given
// secret in its metadata. If no pending identity is found, an api.StatusError is returned with http.StatusNotFound.
func GetPendingTLSIdentityByTokenSecret(ctx context.Context, tx *sql.Tx, secret string) (*Identity, error) {
	identities, err := getIdentitysRaw(ctx, tx, getPendingTLSIdentityByTokenSecretStmt, secret)
	if err != nil {
		return nil, err
	}

	if len(identities) == 0 {
		return nil, api.NewStatusError(http.StatusNotFound, "No pending identities found with given secret")
	} else if len(identities) > 1 {
		return nil, errors.New("Multiple pending identities found with given secret")
	}

	return &identities[0], nil
}

var getOIDCIdentityBySessionIDStmt = fmt.Sprintf(`
SELECT identities.id, identities.auth_method, identities.type, identities.identifier, identities.name, identities.metadata
	FROM identities
	WHERE identities.type = %d
	AND json_extract(identities.metadata, '$.session_id') = ?
`, identityTypeOIDCClient)

// GetOIDCIdentityBySessionID gets a single identity of type identityTypeOIDCClient with the given session ID in its
// metadata. If no identity with this session ID is found, an api.StatusError is returned with http.StatusNotFound.
func GetOIDCIdentityBySessionID(ctx context.Context, tx *sql.Tx, sessionID uuid.UUID) (*Identity, error) {
	identities, err := getIdentitysRaw(ctx, tx, getOIDCIdentityBySessionIDStmt, sessionID.String())
	if err != nil {
		return nil, err
	}

	if len(identities) == 0 {
		return nil, api.NewStatusError(http.StatusNotFound, "No identities found with given session ID")
	} else if len(identities) > 1 {
		return nil, errors.New("Multiple pending identities found with given session ID")
	}

	return &identities[0], nil
}

// GetAuthGroupsByIdentityID returns a slice of groups that the identity with the given ID is a member of.
func GetAuthGroupsByIdentityID(ctx context.Context, tx *sql.Tx, identityID int) ([]AuthGroup, error) {
	stmt := `
SELECT auth_groups.id, auth_groups.name, auth_groups.description
FROM auth_groups
JOIN identities_auth_groups ON auth_groups.id = identities_auth_groups.auth_group_id
WHERE identities_auth_groups.identity_id = ?`

	var result []AuthGroup
	dest := func(scan func(dest ...any) error) error {
		g := AuthGroup{}
		err := scan(&g.ID, &g.Name, &g.Description)
		if err != nil {
			return err
		}

		result = append(result, g)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest, identityID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get groups for identity with ID `%d`: %w", identityID, err)
	}

	return result, nil
}

// GetAllAuthGroupsByIdentityIDs returns a map of identity ID to slice of groups the identity with that ID is a member of.
func GetAllAuthGroupsByIdentityIDs(ctx context.Context, tx *sql.Tx) (map[int][]AuthGroup, error) {
	stmt := `
SELECT identities_auth_groups.identity_id, auth_groups.id, auth_groups.name, auth_groups.description
FROM auth_groups
JOIN identities_auth_groups ON auth_groups.id = identities_auth_groups.auth_group_id`

	result := make(map[int][]AuthGroup)
	dest := func(scan func(dest ...any) error) error {
		var identityID int
		g := AuthGroup{}
		err := scan(&identityID, &g.ID, &g.Name, &g.Description)
		if err != nil {
			return err
		}

		result[identityID] = append(result[identityID], g)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get identities for all groups: %w", err)
	}

	return result, nil
}

// GetIdentityByNameOrIdentifier attempts to get an identity by the authentication method and identifier. If that fails
// it will try to use the nameOrID argument as a name and will return the result only if the query matches a single Identity.
// It will return an api.StatusError with http.StatusNotFound if none are found or http.StatusBadRequest if multiple are found.
func GetIdentityByNameOrIdentifier(ctx context.Context, tx *sql.Tx, authenticationMethod string, nameOrID string) (*Identity, error) {
	id, err := GetIdentity(ctx, tx, AuthMethod(authenticationMethod), nameOrID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	} else if err != nil {
		dbAuthMethod := AuthMethod(authenticationMethod)
		identities, err := GetIdentitys(ctx, tx, IdentityFilter{
			AuthMethod: &dbAuthMethod,
			Name:       &nameOrID,
		})
		if err != nil {
			return nil, err
		}

		if len(identities) == 0 {
			return nil, api.StatusErrorf(http.StatusNotFound, "No identity found with name or identifier %q", nameOrID)
		} else if len(identities) > 1 {
			return nil, api.StatusErrorf(http.StatusBadRequest, "More than one identity found with name %q", nameOrID)
		}

		id = &identities[0]
	}

	return id, nil
}

// SetIdentityAuthGroups deletes all auth_group -> identity mappings from the `identities_auth_groups` table
// where the identity ID is equal to the given value. Then it inserts new associations into the table where the
// group IDs correspond to the given group names.
func SetIdentityAuthGroups(ctx context.Context, tx *sql.Tx, identityID int, groupNames []string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM identities_auth_groups WHERE identity_id = ?`, identityID)
	if err != nil {
		return fmt.Errorf("Failed to delete existing groups for identity with ID `%d`: %w", identityID, err)
	}

	if len(groupNames) == 0 {
		return nil
	}

	args := []any{identityID}
	for _, groupName := range groupNames {
		args = append(args, groupName)
	}

	q := fmt.Sprintf(`
INSERT INTO identities_auth_groups (identity_id, auth_group_id)
SELECT ?, auth_groups.id
FROM auth_groups
WHERE auth_groups.name IN %s
`, query.Params(len(groupNames)))

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("Failed to write identity auth group associations: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to check validity of identity auth group associations: %w", err)
	}

	if int(rowsAffected) != len(groupNames) {
		// No longer on happy "fast" path.
		// Check each group name exists to return a nice error
		missingGroups := make([]string, 0, len(groupNames))
		for _, groupName := range groupNames {
			exists, err := AuthGroupExists(ctx, tx, groupName)
			if err != nil {
				return err
			}

			if !exists {
				missingGroups = append(missingGroups, `"`+groupName+`"`)
			}
		}

		if len(missingGroups) > 0 {
			return api.NewStatusError(http.StatusNotFound, "One or more groups were not found: "+strings.Join(missingGroups, ", "))
		}

		return fmt.Errorf("Failed to write expected number of rows to identity auth group association table (expected %d, got %d)", len(groupNames), rowsAffected)
	}

	return nil
}
