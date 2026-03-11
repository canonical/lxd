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
	"slices"
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

// AuthMethod is a database representation of an authentication method.
//
// AuthMethod is defined on string so that API constants can be converted by casting. The [sql.Scanner] and
// [driver.Valuer] interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an [AuthMethod] as they are read from the
// database. It is not possible to read/write an invalid authentication methods from/to the database when using this type.
type AuthMethod string

const (
	authMethodTLS    int64 = 1
	authMethodOIDC   int64 = 2
	authMethodBearer int64 = 3
)

// authMethodCodeToText maps the database code for an authentication method to it's string representation.
var authMethodCodeToText = map[int64]string{
	authMethodTLS:    api.AuthenticationMethodTLS,
	authMethodOIDC:   api.AuthenticationMethodOIDC,
	authMethodBearer: api.AuthenticationMethodBearer,
}

// ScanInteger implements [query.IntegerScanner] for [AuthMethod]. This simplifies the Scan implementation.
func (a *AuthMethod) ScanInteger(authMethodCode int64) error {
	text, ok := authMethodCodeToText[authMethodCode]
	if !ok {
		return fmt.Errorf("Unknown authentication method %d", authMethodCode)
	}

	*a = AuthMethod(text)
	return nil
}

// Scan implements [sql.Scanner] for [AuthMethod]. This converts the integer value back into the correct API constant or
// returns an error.
func (a *AuthMethod) Scan(value any) error {
	return query.ScanValue(value, a, false)
}

// Value implements [driver.Valuer] for [AuthMethod]. This converts the API constant into an integer or throws an error.
func (a AuthMethod) Value() (driver.Value, error) {
	switch a {
	case api.AuthenticationMethodTLS:
		return authMethodTLS, nil
	case api.AuthenticationMethodOIDC:
		return authMethodOIDC, nil
	case api.AuthenticationMethodBearer:
		return authMethodBearer, nil
	}

	return nil, fmt.Errorf("Invalid authentication method %q", a)
}

// IdentityType indicates the type of the identity.
//
// IdentityType is defined on string so that API constants can be converted by casting. The [sql.Scanner] and
// [driver.Valuer] interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an [IdentityType] as they are read from the
// database. It is not possible to read/write an invalid identity types from/to the database when using this type.
type IdentityType string

// ScanInteger implements [query.IntegerScanner] for [IdentityType]. This simplifies the Scan implementation.
func (i *IdentityType) ScanInteger(identityTypeCode int64) error {
	idType, err := identity.NewFromCode(identityTypeCode)
	if err != nil {
		return err
	}

	*i = IdentityType(idType.Name())

	return nil
}

// Scan implements [sql.Scanner] for [IdentityType]. This converts the integer value back into the correct API constant or
// returns an error.
func (i *IdentityType) Scan(value any) error {
	return query.ScanValue(value, i, false)
}

// Value implements [driver.Valuer] for [IdentityType]. This converts the API constant into an integer or throws an error.
func (i IdentityType) Value() (driver.Value, error) {
	idType, err := identity.New(string(i))
	if err != nil {
		return nil, err
	}

	return idType.Code(), nil
}

// ActiveType returns the active version of the identity type.
func (i IdentityType) ActiveType() (IdentityType, error) {
	switch i {
	case api.IdentityTypeCertificateClientPending:
		return api.IdentityTypeCertificateClient, nil
	case api.IdentityTypeCertificateClusterLinkPending:
		return api.IdentityTypeCertificateClusterLink, nil
	default:
		return "", fmt.Errorf("Identities of type %q cannot be activated", i)
	}
}

// toCertificateAPIType returns the API equivalent type.
func (i IdentityType) toCertificateType() (certificate.Type, error) {
	idType, err := identity.New(string(i))
	if err != nil {
		return -1, err
	}

	certType, err := idType.LegacyCertificateType()
	if err != nil {
		return -1, fmt.Errorf("Identity type %q is not a certificate", i)
	}

	return certType, nil
}

// Identity is a database representation of any authenticated party.
// db:model identities
type Identity struct {
	ID         int64        `db:"id"`
	AuthMethod AuthMethod   `db:"auth_method"`
	Type       IdentityType `db:"type"`
	Identifier string       `db:"identifier"`
	Name       string       `db:"name"`
	Metadata   string       `db:"metadata"`

	// db:join LEFT JOIN identities_certificates ON identities.id = identities_certificates.identity_id
	CertificateID int64 `db:"coalesce(identities_certificates.certificate_id, 0) AS certificate_id"`

	// db:join LEFT JOIN certificates ON identities_certificates.certificate_id = certificates.id
	Certificate string `db:"coalesce(certificates.certificate, '') AS certificate"`
}

// X509 returns an [x509.Certificate] from the [Identity.Certificate].
func (i Identity) X509() (*x509.Certificate, error) {
	certBlock, _ := pem.Decode([]byte(i.Certificate))
	if certBlock == nil {
		return nil, errors.New("Failed decoding certificate")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing certificate: %w", err)
	}

	return cert, nil
}

// ToCertificate converts an [Identity] to a [Certificate].
func (i Identity) ToCertificate() (*CertificateLegacy, error) {
	certificateType, err := i.Type.toCertificateType()
	if err != nil {
		return nil, fmt.Errorf("Failed converting identity type to certificate type: %w", err)
	}

	identityType, err := identity.New(string(i.Type))
	if err != nil {
		return nil, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	isRestricted := !identityType.IsAdmin()

	// Metrics certificates can be both restricted and unrestricted.
	// But an unrestricted metrics certificate has still less permissions as an unrestricted client certificate.
	// So it does not have full access to LXD only the metrics endpoint.
	if i.Type == api.IdentityTypeCertificateMetricsUnrestricted {
		isRestricted = false
	}

	c := &CertificateLegacy{
		ID:          i.ID,
		Fingerprint: i.Identifier,
		Type:        certificateType,
		Name:        i.Name,
		Certificate: i.Certificate,
		Restricted:  isRestricted,
	}

	return c, nil
}

// OIDCMetadata contains metadata for OIDC identities.
type OIDCMetadata struct {
	Subject                string   `json:"subject"`
	IdentityProviderGroups []string `json:"identity_provider_groups"`
}

// Equals returns true if the given [OIDCMetadata] is equal to the receiver.
func (o OIDCMetadata) Equals(m OIDCMetadata) bool {
	if o.Subject != m.Subject {
		return false
	}

	slices.Sort(o.IdentityProviderGroups)
	slices.Sort(m.IdentityProviderGroups)
	return slices.Equal(o.IdentityProviderGroups, m.IdentityProviderGroups)
}

// OIDCMetadata returns the identity metadata as [OIDCMetadata]. The [AuthMethod] of the [Identity] must be [api.AuthenticationMethodOIDC].
func (i Identity) OIDCMetadata() (*OIDCMetadata, error) {
	if i.AuthMethod != api.AuthenticationMethodOIDC {
		return nil, fmt.Errorf("Cannot extract OIDC metadata from identity: Identity has authentication method %q (%q required)", i.AuthMethod, api.AuthenticationMethodOIDC)
	}

	var metadata OIDCMetadata
	err := json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal OIDC metadata: %w", err)
	}

	return &metadata, nil
}

// PendingTLSMetadata contains metadata for the pending TLS certificate identity type.
type PendingTLSMetadata struct {
	Secret string    `json:"secret"`
	Expiry time.Time `json:"expiry"`
}

// PendingTLSMetadata returns the pending TLS identity metadata.
func (i Identity) PendingTLSMetadata() (*PendingTLSMetadata, error) {
	identityType, err := identity.New(string(i.Type))
	if err != nil {
		return nil, err
	}

	if !identityType.IsPending() {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Cannot extract pending %q TLS identity secret: Identity is not pending", i.Type)
	}

	var metadata PendingTLSMetadata
	err = json.Unmarshal([]byte(i.Metadata), &metadata)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed to unmarshal pending TLS identity metadata: %w", err)
	}

	return &metadata, nil
}

// ToAPI converts an [Identity] to an [api.Identity], executing database queries as necessary.
func (i *Identity) ToAPI(ctx context.Context, tx *sql.Tx, canViewGroup auth.PermissionChecker) (*api.Identity, error) {
	groups, err := GetAuthGroupsByIdentityID(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	groupNames := make([]string, 0, len(groups))
	for _, group := range groups {
		if canViewGroup(entity.AuthGroupURL(group.Name)) {
			groupNames = append(groupNames, group.Name)
		}
	}

	return &api.Identity{
		AuthenticationMethod: string(i.AuthMethod),
		Type:                 string(i.Type),
		Identifier:           i.Identifier,
		Name:                 i.Name,
		Groups:               groupNames,
		TLSCertificate:       i.Certificate,
	}, nil
}

// GetIdentityByAuthenticationMethodAndIdentifier gets a single identity by authentication method and identifier.
func GetIdentityByAuthenticationMethodAndIdentifier(ctx context.Context, tx *sql.Tx, authenticationMethod string, identifier string) (*Identity, error) {
	return query.SelectOne[Identity](ctx, tx, "WHERE auth_method = ? AND identifier = ?", AuthMethod(authenticationMethod), identifier)
}

// DeleteIdentityByNameAndType deletes a single identity with the given name and type.
// Note that the name of an identity is not guaranteed to be unique for OIDC identities.
func DeleteIdentityByNameAndType(ctx context.Context, tx *sql.Tx, name string, identityType string) error {
	return query.DeleteOne[Identity](ctx, tx, "WHERE name = ? AND type = ?", name, IdentityType(identityType))
}

// DeleteIdentityByAuthenticationMethodAndIdentifier deletes a single identity with the given authentication method and identifier.
func DeleteIdentityByAuthenticationMethodAndIdentifier(ctx context.Context, tx *sql.Tx, authenticationMethod string, identifier string) error {
	return query.DeleteOne[Identity](ctx, tx, "WHERE auth_method = ? AND identifier = ?", AuthMethod(authenticationMethod), identifier)
}

// ActivateTLSIdentity updates a TLS identity to make it valid by adding the fingerprint, PEM encoded certificate, and setting
// the type.
func ActivateTLSIdentity(ctx context.Context, tx *sql.Tx, identifier uuid.UUID, cert *x509.Certificate) error {
	fingerprint := shared.CertFingerprint(cert)
	_, err := GetIdentityByAuthenticationMethodAndIdentifier(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err == nil {
		return api.StatusErrorf(http.StatusConflict, "Identity already exists")
	}

	id, err := GetIdentityByAuthenticationMethodAndIdentifier(ctx, tx, api.AuthenticationMethodTLS, identifier.String())
	if err != nil {
		return fmt.Errorf("Failed getting pending TLS identity: %w", err)
	}

	certID, err := query.Create(ctx, tx, Certificate{Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))})
	if err != nil {
		return fmt.Errorf("Failed creating certificate for pending TLS identity: %w", err)
	}

	_, err = tx.ExecContext(ctx, "INSERT INTO identities_certificates (identity_id, certificate_id) VALUES (?, ?)", id.ID, certID)
	if err != nil {
		return fmt.Errorf("Failed associating identity with certificate: %w", err)
	}

	identityTypeActive, err := id.Type.ActiveType()
	if err != nil {
		return err
	}

	id.Type = identityTypeActive
	id.Identifier = fingerprint
	return query.Update(ctx, tx, id)
}

var pendingIdentityTypes = func() (result []int64) {
	for _, t := range identity.Types() {
		if t.IsPending() {
			result = append(result, t.Code())
		}
	}

	return result
}

// GetPendingTLSIdentityByTokenSecret gets a single identity of type [identityTypeCertificateClientPending] or [identityTypeCertificateClusterLinkPending] with the given secret in its metadata. If no pending identity is found, an [api.StatusError] is returned with [http.StatusNotFound].
func GetPendingTLSIdentityByTokenSecret(ctx context.Context, tx *sql.Tx, secret string) (*Identity, error) {
	clause := fmt.Sprintf(`
	WHERE identities.type IN %s
	AND json_extract(identities.metadata, '$.secret') = ?`, query.IntParams(pendingIdentityTypes()...))

	id, err := query.SelectOne[Identity](ctx, tx, clause, secret)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Maintain error message for clarity.
			return nil, api.NewStatusError(http.StatusNotFound, "No pending identities found with given secret")
		}

		return nil, fmt.Errorf("Failed getting identity by token secret: %w", err)
	}

	return id, nil
}

// GetAuthGroupsByIdentityID returns a slice of groups that the identity with the given ID is a member of.
func GetAuthGroupsByIdentityID(ctx context.Context, tx *sql.Tx, identityID int64) ([]AuthGroup, error) {
	clause := `
JOIN identities_auth_groups ON auth_groups.id = identities_auth_groups.auth_group_id
WHERE identities_auth_groups.identity_id = ?
`
	return query.Select[AuthGroup](ctx, tx, clause, identityID)
}

// GetAllAuthGroupsByIdentityIDs returns a map of identity ID to slice of groups the identity with that ID is a member of.
func GetAllAuthGroupsByIdentityIDs(ctx context.Context, tx *sql.Tx) (map[int64][]AuthGroup, error) {
	stmt := `
SELECT identities_auth_groups.identity_id, auth_groups.id, auth_groups.name, auth_groups.description
FROM auth_groups
JOIN identities_auth_groups ON auth_groups.id = identities_auth_groups.auth_group_id`

	result := make(map[int64][]AuthGroup)
	dest := func(scan func(dest ...any) error) error {
		var identityID int64
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
// It will return an [api.StatusError] with [http.StatusNotFound] if none are found or [http.StatusBadRequest] if multiple are found.
func GetIdentityByNameOrIdentifier(ctx context.Context, tx *sql.Tx, authenticationMethod string, nameOrID string) (*Identity, error) {
	id, err := GetIdentityByAuthenticationMethodAndIdentifier(ctx, tx, authenticationMethod, nameOrID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	} else if err != nil {
		return query.SelectOne[Identity](ctx, tx, "WHERE auth_method = ? AND name = ?", AuthMethod(authenticationMethod), nameOrID)
	}

	return id, nil
}

// SetIdentityAuthGroups deletes all auth_group -> identity mappings from the `identities_auth_groups` table
// where the identity ID is equal to the given value. Then it inserts new associations into the table where the
// group IDs correspond to the given group names.
func SetIdentityAuthGroups(ctx context.Context, tx *sql.Tx, identityID int64, groupNames []string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM identities_auth_groups WHERE identity_id = ?`, identityID)
	if err != nil {
		return fmt.Errorf("Failed to delete existing groups for identity with ID %d: %w", identityID, err)
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

// GetIdentityByID gets a single identity with the given ID.
func GetIdentityByID(ctx context.Context, tx *sql.Tx, id int64) (*Identity, error) {
	return query.SelectOne[Identity](ctx, tx, "WHERE identities.id = ?", id)
}

// UpdateIdentityCertificate replaces an identities certificate with the given one.
func UpdateIdentityCertificate(ctx context.Context, tx *sql.Tx, id Identity, cert x509.Certificate) error {
	certificateID := id.CertificateID
	if certificateID == 0 {
		clause := `JOIN identities_certificates ON certificates.id = identities_certificates.certificate_id WHERE identities_certificates.identity_id = ?`
		dbCert, err := query.SelectOne[Certificate](ctx, tx, clause, id.ID)
		if err != nil {
			return fmt.Errorf("Failed getting certificate associated with identity: %w", err)
		}

		certificateID = dbCert.ID
	}

	certToUpdate := Certificate{
		ID:          certificateID,
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
	}

	err := query.Update(ctx, tx, certToUpdate)
	if err != nil {
		return fmt.Errorf("Failed updating certificate: %w", err)
	}

	id.Identifier = shared.CertFingerprint(&cert)
	err = query.Update(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("Failed updating identity identifier: %w", err)
	}

	return nil
}
