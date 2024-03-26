//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// Certificate is here to pass the certificates content from the database around.
type Certificate struct {
	ID          int
	Fingerprint string `db:"primary=yes"`
	Type        certificate.Type
	Name        string
	Certificate string
	Restricted  bool
}

// CertificateFilter specifies potential query parameter fields.
type CertificateFilter struct {
	ID          *int
	Fingerprint *string
	Name        *string
	Type        *certificate.Type
}

// ToAPIType returns the API equivalent type.
func (cert *Certificate) ToAPIType() string {
	switch cert.Type {
	case certificate.TypeClient:
		return api.CertificateTypeClient
	case certificate.TypeServer:
		return api.CertificateTypeServer
	case certificate.TypeMetrics:
		return api.CertificateTypeMetrics
	}

	return api.CertificateTypeUnknown
}

// ToIdentityType returns a suitable IdentityType for the certificate.
func (cert *Certificate) ToIdentityType() (IdentityType, error) {
	switch cert.Type {
	case certificate.TypeClient:
		if cert.Restricted {
			return api.IdentityTypeCertificateClientRestricted, nil
		}

		return api.IdentityTypeCertificateClientUnrestricted, nil
	case certificate.TypeServer:
		return api.IdentityTypeCertificateServer, nil
	case certificate.TypeMetrics:
		if cert.Restricted {
			return api.IdentityTypeCertificateMetricsRestricted, nil
		}

		return api.IdentityTypeCertificateMetricsUnrestricted, nil
	}

	return "", fmt.Errorf("Unknown certificate type `%d`", cert.Type)
}

// ToAPI converts the database Certificate struct to an api.Certificate
// entry filling fields from the database as necessary.
func (cert *Certificate) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Certificate, error) {
	resp := api.Certificate{}
	resp.Fingerprint = cert.Fingerprint
	resp.Certificate = cert.Certificate
	resp.Name = cert.Name
	resp.Restricted = cert.Restricted
	resp.Type = cert.ToAPIType()

	projects, err := GetCertificateProjects(ctx, tx, cert.ID)
	if err != nil {
		return nil, err
	}

	resp.Projects = make([]string, len(projects))
	for i, p := range projects {
		resp.Projects[i] = p.Name
	}

	return &resp, nil
}

// ToIdentity converts a Certificate to an Identity.
func (cert Certificate) ToIdentity() (*Identity, error) {
	identityType, err := cert.ToIdentityType()
	if err != nil {
		return nil, fmt.Errorf("Failed converting certificate to identity: %w", err)
	}

	b, err := json.Marshal(CertificateMetadata{Certificate: cert.Certificate})
	if err != nil {
		return nil, fmt.Errorf("Failed converting certificate to identity: %w", err)
	}

	identity := &Identity{
		ID:         cert.ID,
		AuthMethod: AuthMethod(api.AuthenticationMethodTLS),
		Type:       identityType,
		Identifier: cert.Fingerprint,
		Name:       cert.Name,
		Metadata:   string(b),
	}

	return identity, nil
}

// GetCertificateByFingerprintPrefix gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func GetCertificateByFingerprintPrefix(ctx context.Context, tx *sql.Tx, fingerprintPrefix string) (*Certificate, error) {
	var err error
	var cert *Certificate
	sql := `
SELECT identities.identifier
	FROM identities
	WHERE identities.identifier LIKE ? AND auth_method = ?
	ORDER BY identities.identifier
		`

	fingerprints, err := query.SelectStrings(ctx, tx, sql, fingerprintPrefix+"%", AuthMethod(api.AuthenticationMethodTLS))
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch certificates fingerprints matching prefix %q: %w", fingerprintPrefix, err)
	}

	if len(fingerprints) > 1 {
		return nil, fmt.Errorf("More than one certificate matches")
	}

	if len(fingerprints) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Certificate not found")
	}

	cert, err = GetCertificate(ctx, tx, fingerprints[0])
	if err != nil {
		return nil, err
	}

	return cert, nil
}

// CreateCertificateWithProjects stores a CertInfo object in the db, and associates it to a list of project names.
// It will ignore the ID field from the CertInfo.
func CreateCertificateWithProjects(ctx context.Context, tx *sql.Tx, cert Certificate, projectNames []string) (int64, error) {
	var id int64
	var err error
	id, err = CreateCertificate(ctx, tx, cert)
	if err != nil {
		return -1, err
	}

	err = UpdateCertificateProjects(ctx, tx, int(id), projectNames)
	if err != nil {
		return -1, err
	}

	return id, err
}

// GetCertificates returns all available certificates.
func GetCertificates(ctx context.Context, tx *sql.Tx, filters ...CertificateFilter) ([]Certificate, error) {
	authMethod := AuthMethod(api.AuthenticationMethodTLS)
	identityFilters := make([]IdentityFilter, 0, len(filters))
	for _, filter := range filters {
		var identityTypes []IdentityType
		if filter.Type != nil {
			switch *filter.Type {
			case certificate.TypeClient:
				identityTypes = append(identityTypes, api.IdentityTypeCertificateClientRestricted, api.IdentityTypeCertificateClientUnrestricted)
			case certificate.TypeServer:
				identityTypes = append(identityTypes, api.IdentityTypeCertificateServer)
			case certificate.TypeMetrics:
				identityTypes = append(identityTypes, api.IdentityTypeCertificateMetricsRestricted, api.IdentityTypeCertificateMetricsUnrestricted)
			}
		}

		for _, identityType := range identityTypes {
			idType := identityType
			identityFilters = append(identityFilters, IdentityFilter{
				ID:         filter.ID,
				AuthMethod: &authMethod,
				Type:       &idType,
				Identifier: filter.Fingerprint,
				Name:       filter.Name,
			})
		}
	}

	if len(identityFilters) == 0 {
		identityFilters = []IdentityFilter{
			{
				AuthMethod: &authMethod,
			},
		}
	}

	certificateIdentities, err := GetIdentitys(ctx, tx, identityFilters...)
	if err != nil {
		return nil, err
	}

	certificates := make([]Certificate, 0, len(certificateIdentities))
	for _, certificateIdentity := range certificateIdentities {
		cert, err := certificateIdentity.ToCertificate()
		if err != nil {
			return nil, err
		}

		certificates = append(certificates, *cert)
	}

	return certificates, nil
}

// GetCertificate returns the certificate with the given key.
func GetCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) (*Certificate, error) {
	identity, err := GetIdentity(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err != nil {
		return nil, err
	}

	return identity.ToCertificate()
}

// GetCertificateID return the ID of the certificate with the given key.
func GetCertificateID(ctx context.Context, tx *sql.Tx, fingerprint string) (int64, error) {
	identity, err := GetIdentity(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err != nil {
		return 0, err
	}

	return int64(identity.ID), nil
}

// CertificateExists checks if a certificate with the given key exists.
func CertificateExists(ctx context.Context, tx *sql.Tx, fingerprint string) (bool, error) {
	_, err := GetIdentity(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return false, err
	} else if err != nil {
		return false, nil
	}

	return true, nil
}

// CreateCertificate adds a new certificate to the database.
func CreateCertificate(ctx context.Context, tx *sql.Tx, object Certificate) (int64, error) {
	identity, err := object.ToIdentity()
	if err != nil {
		return 0, err
	}

	return CreateIdentity(ctx, tx, *identity)
}

// DeleteCertificate deletes the certificate matching the given key parameters.
func DeleteCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) error {
	return DeleteIdentity(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
}

// DeleteCertificates deletes the certificate matching the given key parameters.
func DeleteCertificates(ctx context.Context, tx *sql.Tx, name string, certificateType certificate.Type) error {
	if certificateType == certificate.TypeClient {
		err := DeleteIdentitys(ctx, tx, name, api.IdentityTypeCertificateClientRestricted)
		if err != nil {
			return err
		}

		return DeleteIdentitys(ctx, tx, name, api.IdentityTypeCertificateClientUnrestricted)
	} else if certificateType == certificate.TypeServer {
		return DeleteIdentitys(ctx, tx, name, api.IdentityTypeCertificateServer)
	} else if certificateType == certificate.TypeMetrics {
		err := DeleteIdentitys(ctx, tx, name, api.IdentityTypeCertificateMetricsRestricted)
		if err != nil {
			return err
		}

		return DeleteIdentitys(ctx, tx, name, api.IdentityTypeCertificateMetricsUnrestricted)
	}

	return nil
}

// UpdateCertificate updates the certificate matching the given key parameters.
func UpdateCertificate(ctx context.Context, tx *sql.Tx, fingerprint string, object Certificate) error {
	identity, err := object.ToIdentity()
	if err != nil {
		return err
	}

	return UpdateIdentity(ctx, tx, AuthMethod(api.AuthenticationMethodTLS), fingerprint, *identity)
}
