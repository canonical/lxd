//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// CertificateLegacy represents a certificate as queried in the legacy /1.0/certificates API.
type CertificateLegacy struct {
	ID          int64
	Fingerprint string `db:"primary=yes"`
	Type        certificate.Type
	Name        string
	Certificate string
	Restricted  bool
}

// ToAPIType returns the API equivalent type.
func (cert *CertificateLegacy) ToAPIType() string {
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
func (cert *CertificateLegacy) ToIdentityType() (IdentityType, error) {
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

	return "", fmt.Errorf("Unknown certificate type %d", cert.Type)
}

// ToAPI converts the database Certificate struct to an api.Certificate
// entry filling fields from the database as necessary.
func (cert *CertificateLegacy) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Certificate, error) {
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

// GetCertificateProjects returns a slice of [Project] that the [Certificate] with the given ID is related to.
// This is only valid for restricted legacy certificates.
func GetCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int64) ([]Project, error) {
	q := `
SELECT projects.id, projects.description, projects.name FROM projects 
    JOIN identities_projects ON projects.id = identities_projects.project_id 
	WHERE identities_projects.identity_id = ? 
	ORDER BY projects.name
`
	return getProjectsRaw(ctx, tx, q, certificateID)
}

// ToIdentity converts a Certificate to an Identity.
func (cert CertificateLegacy) ToIdentity() (*Identity, error) {
	identityType, err := cert.ToIdentityType()
	if err != nil {
		return nil, fmt.Errorf("Failed converting certificate to identity: %w", err)
	}

	identity := &Identity{
		ID:          cert.ID,
		AuthMethod:  AuthMethod(api.AuthenticationMethodTLS),
		Type:        identityType,
		Identifier:  cert.Fingerprint,
		Name:        cert.Name,
		Certificate: cert.Certificate,
	}

	return identity, nil
}

var getCertificateIdentitiesClause = `
	WHERE auth_method = ` + strconv.Itoa(int(authMethodTLS)) + `
	AND type in ` + query.IntParams(certIdentityTypes()...)

// GetCertificateByFingerprintPrefix gets a Certificate from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func GetCertificateByFingerprintPrefix(ctx context.Context, tx *sql.Tx, fingerprintPrefix string) (*CertificateLegacy, error) {
	id, err := query.SelectOne[Identity](ctx, tx, getCertificateIdentitiesClause+" AND identities.identifier LIKE ?", fingerprintPrefix+"%")
	if err != nil {
		return nil, err
	}

	return id.ToCertificate()
}

// CreateCertificateWithProjects stores a Certificate object in the db, and associates it to a list of project names.
// It will ignore the ID field from the Certificate.
func CreateCertificateWithProjects(ctx context.Context, tx *sql.Tx, cert CertificateLegacy, projectNames []string) (int64, error) {
	var id int64
	var err error
	id, err = CreateLegacyCertificate(ctx, tx, cert)
	if err != nil {
		return -1, err
	}

	err = UpdateCertificateProjects(ctx, tx, id, projectNames)
	if err != nil {
		return -1, err
	}

	return id, err
}

// UpdateCertificateProjects deletes and replaces any certificate to project associations.
func UpdateCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID int64, projectNames []string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM identities_projects WHERE identity_id = ?", certificateID)
	if err != nil {
		return fmt.Errorf("Failed to delete existing certificate project relationships: %w", err)
	}

	if len(projectNames) == 0 {
		// No projects to add.
		return nil
	}

	args := make([]any, 0, len(projectNames)+1)
	args = append(args, certificateID)
	for _, name := range projectNames {
		args = append(args, name)
	}

	res, err := tx.ExecContext(ctx, "INSERT INTO identities_projects (identity_id, project_id) SELECT ?, projects.id FROM projects WHERE name IN "+query.Params(len(projectNames)), args...)
	if err != nil {
		return fmt.Errorf("Failed to create certificate project relationships: %w", err)
	}

	nInserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to check certificate update was successful: %w", err)
	}

	if int(nInserted) != len(projectNames) {
		return api.StatusErrorf(http.StatusNotFound, "Project not found")
	}

	return nil
}

// GetLegacyCertificates returns all legacy certificates.
func GetLegacyCertificates(ctx context.Context, tx *sql.Tx) ([]CertificateLegacy, error) {
	certificateIdentities, err := query.Select[Identity](ctx, tx, getCertificateIdentitiesClause)
	if err != nil {
		return nil, err
	}

	certificates := make([]CertificateLegacy, 0, len(certificateIdentities))
	for _, certificateIdentity := range certificateIdentities {
		cert, err := certificateIdentity.ToCertificate()
		if err != nil {
			return nil, err
		}

		certificates = append(certificates, *cert)
	}

	return certificates, nil
}

// GetLegacyCertificate returns the legacy certificate with the given fingerprint.
func GetLegacyCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) (*CertificateLegacy, error) {
	id, err := query.SelectOne[Identity](ctx, tx, getCertificateIdentitiesClause+" AND identities.identifier = ?", fingerprint)
	if err != nil {
		return nil, err
	}

	return id.ToCertificate()
}

// CreateLegacyCertificate creates a new legacy certificate.
func CreateLegacyCertificate(ctx context.Context, tx *sql.Tx, object CertificateLegacy) (int64, error) {
	identity, err := object.ToIdentity()
	if err != nil {
		return 0, err
	}

	return query.Create(ctx, tx, *identity)
}

// DeleteLegacyCertificate deletes the legacy certificate with the given fingerprint.
func DeleteLegacyCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) error {
	return DeleteIdentityByAuthenticationMethodAndIdentifier(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
}

// UpdateLegacyCertificate updates the certificate matching the given key parameters.
func UpdateLegacyCertificate(ctx context.Context, tx *sql.Tx, object CertificateLegacy) error {
	identity, err := object.ToIdentity()
	if err != nil {
		return err
	}

	return query.Update(ctx, tx, identity)
}
