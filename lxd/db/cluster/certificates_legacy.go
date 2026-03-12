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
	"strings"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// Certificate is here to pass the certificates content from the database around.
type Certificate struct {
	ID          int64
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

	return "", fmt.Errorf("Unknown certificate type %d", cert.Type)
}

// ToAPI converts the database Certificate struct to an api.Certificate
// The certificateIDToProjects map must be provided and can be loaded via [GetCertificateProjects].
func (cert *Certificate) ToAPI(certificateIDToProjects map[int64][]string) (*api.Certificate, error) {
	if certificateIDToProjects == nil {
		return nil, errors.New("Missing required certificate project details")
	}

	// If there are no projects, set to an empty slice instead of null to maintain API behaviour.
	// It also makes clear that the field expects an array e.g. when performing `lxc config trust edit`.
	projects, ok := certificateIDToProjects[cert.ID]
	if !ok {
		projects = []string{}
	}

	resp := api.Certificate{}
	resp.Fingerprint = cert.Fingerprint
	resp.Certificate = cert.Certificate
	resp.Name = cert.Name
	resp.Restricted = cert.Restricted
	resp.Type = cert.ToAPIType()
	resp.Projects = projects
	return &resp, nil
}

// GetCertificateProjects returns a map of certificate (identity) ID to list of (alphabetically sorted) project names.
// The output map should only contain IDs of restricted legacy certificates.
// If the optional certificate ID is passed, the result will only contain the given ID.
func GetCertificateProjects(ctx context.Context, tx *sql.Tx, certificateID *int64) (map[int64][]string, error) {
	var b strings.Builder
	var args []any
	b.WriteString(`SELECT identities_projects.identity_id, projects.name FROM projects 
    JOIN identities_projects ON projects.id = identities_projects.project_id 
	`)

	if certificateID != nil {
		args = []any{*certificateID}
		b.WriteString(`WHERE identities_projects.identity_id = ? `)
	}

	// It is important to always return the list of project names in the same order.
	// This is for two reasons:
	// 1. The Etag for a certificate contains this field. A random ordering would lead to inconsistent hashing (and precondition failed errors for clients).
	// 2. When a restricted client certificate updates their own certificate, the API handler checks that the caller has not attempted to change their
	//    accessible projects. It does this with an ordered equality check on the project list.
	b.WriteString(`ORDER BY identities_projects.identity_id, projects.name`)

	out := make(map[int64][]string)
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var identityID int64
		var projectName string
		err := scan(&identityID, &projectName)
		if err != nil {
			return err
		}

		out[identityID] = append(out[identityID], projectName)
		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed loading certificate projects: %w", err)
	}

	return out, nil
}

// ToIdentity converts a Certificate to an [IdentitiesRow].
func (cert Certificate) ToIdentity() (*IdentitiesRow, error) {
	identityType, err := cert.ToIdentityType()
	if err != nil {
		return nil, fmt.Errorf("Failed converting certificate to identity: %w", err)
	}

	b, err := json.Marshal(CertificateMetadata{Certificate: cert.Certificate})
	if err != nil {
		return nil, fmt.Errorf("Failed converting certificate to identity: %w", err)
	}

	identity := &IdentitiesRow{
		ID:         cert.ID,
		AuthMethod: AuthMethod(api.AuthenticationMethodTLS),
		Type:       identityType,
		Identifier: cert.Fingerprint,
		Name:       cert.Name,
		Metadata:   string(b),
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
func GetCertificateByFingerprintPrefix(ctx context.Context, tx *sql.Tx, fingerprintPrefix string) (*Certificate, error) {
	id, err := query.SelectOne[IdentitiesRow](ctx, tx, getCertificateIdentitiesClause+" AND identities.identifier LIKE ?", fingerprintPrefix+"%")
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Overwrite error message sent via API to use "Certificate" instead of "Identity".
			return nil, api.NewStatusError(http.StatusNotFound, "Certificate not found")
		}

		if query.IsMultipleMatchErr(err) {
			// Maintain "certificate" in error message.
			return nil, api.NewStatusError(http.StatusBadRequest, "More than one certificate matches")
		}

		return nil, err
	}

	return id.ToCertificate()
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
		return fmt.Errorf("Failed deleting existing certificate project relationships: %w", err)
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
		return fmt.Errorf("Failed creating certificate project relationships: %w", err)
	}

	nInserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed checking certificate update was successful: %w", err)
	}

	if int(nInserted) != len(projectNames) {
		return api.StatusErrorf(http.StatusNotFound, "Project not found")
	}

	return nil
}

// GetCertificatesAndURLs returns all available certificates and their URLs.
// An optional filter function can be passed to filter the output. It should return true to include a certificate and false to omit it.
func GetCertificatesAndURLs(ctx context.Context, tx *sql.Tx, filter func(Certificate) bool) ([]Certificate, []string, error) {
	var certificates []Certificate
	var urls []string
	err := query.SelectFunc[IdentitiesRow](ctx, tx, getCertificateIdentitiesClause, func(identity IdentitiesRow) error {
		cert, err := identity.ToCertificate()
		if err != nil {
			return err
		}

		if filter != nil && !filter(*cert) {
			return nil
		}

		certificates = append(certificates, *cert)
		urls = append(urls, entity.CertificateURL(cert.Fingerprint).String())
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return certificates, urls, nil
}

// GetCertificate returns the certificate with the given fingerprint.
func GetCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) (*Certificate, error) {
	id, err := query.SelectOne[IdentitiesRow](ctx, tx, getCertificateIdentitiesClause+" AND identities.identifier = ?", fingerprint)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Overwrite error message sent via API to use "Certificate" instead of "Identity".
			return nil, api.NewStatusError(http.StatusNotFound, "Certificate not found")
		}

		// No need to check for multiple matches because of the unique constraint on the identities table which
		// disallows more than one identity with the same authentication method and identifier.
		return nil, err
	}

	return id.ToCertificate()
}

// GetCertificateID returns the ID of the certificate with the given fingerprint.
func GetCertificateID(ctx context.Context, tx *sql.Tx, fingerprint string) (int64, error) {
	cert, err := GetCertificate(ctx, tx, fingerprint)
	if err != nil {
		return 0, err
	}

	return cert.ID, nil
}

// CreateCertificate adds a new certificate to the database.
func CreateCertificate(ctx context.Context, tx *sql.Tx, object Certificate) (int64, error) {
	identity, err := object.ToIdentity()
	if err != nil {
		return 0, err
	}

	id, err := query.Create(ctx, tx, *identity)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			// Overwrite error message sent via API to use "Certificate" instead of "Identity".
			return 0, api.NewStatusError(http.StatusConflict, "Certificate already exists")
		}

		return 0, err
	}

	return id, nil
}

// DeleteCertificate deletes the certificate matching the given key parameters.
func DeleteCertificate(ctx context.Context, tx *sql.Tx, fingerprint string) error {
	err := DeleteIdentityByAuthenticationMethodAndIdentifier(ctx, tx, api.AuthenticationMethodTLS, fingerprint)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Overwrite error message sent via API to use "Certificate" instead of "Identity".
			return api.NewStatusError(http.StatusNotFound, "Certificate not found")
		}

		// No need to check for multiple matches because of the unique constraint on the identities table which
		// disallows more than one identity with the same authentication method and identifier.
		return err
	}

	return nil
}

// UpdateCertificate updates the certificate matching the given key parameters.
func UpdateCertificate(ctx context.Context, tx *sql.Tx, object Certificate) error {
	identity, err := object.ToIdentity()
	if err != nil {
		return err
	}

	return query.UpdateByPrimaryKey(ctx, tx, identity)
}
