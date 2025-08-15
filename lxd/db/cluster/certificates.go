//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

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

var getCertificateIdentitiesStmt = `
SELECT identities.id, identities.auth_method, identities.type, identities.identifier, identities.name, identities.metadata
	FROM identities
	WHERE auth_method = ` + strconv.Itoa(int(authMethodTLS)) + `
	AND type in ` + query.IntParams(certIdentityTypes()...)

// GetCertificateByFingerprintPrefix gets a Certificate from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func GetCertificateByFingerprintPrefix(ctx context.Context, tx *sql.Tx, fingerprintPrefix string) (*Certificate, error) {
	dbCertificateIdentities, err := getIdentitysRaw(ctx, tx, getCertificateIdentitiesStmt+" AND identities.identifier LIKE ?", fingerprintPrefix+"%")
	if err != nil {
		return nil, err
	}

	if len(dbCertificateIdentities) > 1 {
		return nil, errors.New("More than one certificate matches")
	}

	if len(dbCertificateIdentities) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Certificate not found")
	}

	return dbCertificateIdentities[0].ToCertificate()
}

// CreateCertificateWithProjects stores a Certificate object in the db, and associates it to a list of project names.
// It will ignore the ID field from the Certificate.
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
func GetCertificates(ctx context.Context, tx *sql.Tx) ([]Certificate, error) {
	certificateIdentities, err := getIdentitysRaw(ctx, tx, getCertificateIdentitiesStmt)
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

// GetCertificate returns the certificate with the given fingerprint.
func GetCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) (*Certificate, error) {
	dbCertificateIdentities, err := getIdentitysRaw(ctx, tx, getCertificateIdentitiesStmt+" AND identities.identifier = ?", fingerprint)
	if err != nil {
		return nil, err
	}

	if len(dbCertificateIdentities) == 0 {
		return nil, api.NewStatusError(http.StatusNotFound, "Certificate not found")
	} else if len(dbCertificateIdentities) > 1 {
		return nil, fmt.Errorf("More than one certificate with fingerprint %q", fingerprint)
	}

	return dbCertificateIdentities[0].ToCertificate()
}

// GetCertificateID returns the ID of the certificate with the given fingerprint.
func GetCertificateID(ctx context.Context, tx *sql.Tx, fingerprint string) (int64, error) {
	cert, err := GetCertificate(ctx, tx, fingerprint)
	if err != nil {
		return 0, err
	}

	return int64(cert.ID), nil
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

// UpdateCertificate updates the certificate matching the given key parameters.
func UpdateCertificate(ctx context.Context, tx *sql.Tx, fingerprint string, object Certificate) error {
	identity, err := object.ToIdentity()
	if err != nil {
		return err
	}

	return UpdateIdentity(ctx, tx, api.AuthenticationMethodTLS, fingerprint, *identity)
}
