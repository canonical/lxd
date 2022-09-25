//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t certificates.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e certificate objects
//go:generate mapper stmt -e certificate objects-by-ID
//go:generate mapper stmt -e certificate objects-by-Fingerprint
//go:generate mapper stmt -e certificate id
//go:generate mapper stmt -e certificate create struct=Certificate
//go:generate mapper stmt -e certificate delete-by-Fingerprint
//go:generate mapper stmt -e certificate delete-by-Name-and-Type
//go:generate mapper stmt -e certificate update struct=Certificate
//
//go:generate mapper method -i -e certificate GetMany
//go:generate mapper method -i -e certificate GetOne
//go:generate mapper method -i -e certificate ID struct=Certificate
//go:generate mapper method -i -e certificate Exists struct=Certificate
//go:generate mapper method -i -e certificate Create struct=Certificate
//go:generate mapper method -i -e certificate DeleteOne-by-Fingerprint
//go:generate mapper method -i -e certificate DeleteMany-by-Name-and-Type
//go:generate mapper method -i -e certificate Update struct=Certificate

// New naming structure for object stmts.
var selectCertificateObjects = certificateObjects
var selectCertificateObjectsByID = certificateObjectsByID
var selectCertificateObjectsByFingerprint = certificateObjectsByFingerprint

// A handwritten statement fetching all the certificates that belong to a project.
// This statement has the label "InAnyProject", but no extra arguments.
// This will be considered in a generated function called `GetCertificatesInAnyProject`.
var selectCertificateObjectsInAnyProject = RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  JOIN certificates_projects ON certificate.id = certificates_projects.certificate_id
  ORDER BY certificates.fingerprint
`)

// A handwritten statement fetching all the certificates matching the project.
// This statement has no label, but has the extra argument "ProjectID".
// This will be considered in a generated function called `GetCertificatesWithProjectID`.
// The generated function will also have a `projectID` argument option.
var selectCertificateObjectsWithProjectIDByFingerprint = RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  JOIN certificates_projects ON certificate.id = certificates_projects.certificate_id
  WHERE ( certificate_projects.project_id = ? AND certificates.fingerprint = ? )
  ORDER BY certificates.fingerprint
`)

// A handwritten statement fetching all the server type certificates matching the project.
// This statement has label "OfTypeServer" and the extra argument "ProjectID".
// This statement will be considered in a generated function called `GetCertificatesOfTypeServerWithProjectID`.
// The generated function will also have a `projectID` argument option.
var selectCertificateObjectsOfTypeServerWithProjectID = RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  JOIN certificates_projects ON certificate.id = certificates_projects.certificate_id
  WHERE ( certificate_projects.project_id = ? AND certificates.type = 2 )
  ORDER BY certificates.fingerprint
`)

// This statement will be considered in the same `GetCertificatesOfTypeServerWithProjectID` function as above.
// The `ByFingerprint` suffix will update this function to allow a query if only the `Fingerprint` field of the
// given `CertificateFilter`s is not nil.
var selectCertificateObjectsOfTypeServerWithProjectIDByFingerprint = RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  JOIN certificates_projects ON certificate.id = certificates_projects.certificate_id
  WHERE ( certificate_projects.project_id = ? AND certificates.type = 2 AND certificates.fingerprint = ? )
  ORDER BY certificates.fingerprint
`)

// Certificate is here to pass the certificates content from the database around.
type Certificate struct {
	ID          int
	Fingerprint string `db:"primary=yes"`
	Type        CertificateType
	Name        string
	Certificate string
	Restricted  bool
}

// CertificateFilter specifies potential query parameter fields.
type CertificateFilter struct {
	ID          *int
	Fingerprint *string
	Name        *string
	Type        *CertificateType
}

// CertificateType indicates the type of the certificate.
type CertificateType int

// CertificateTypeClient indicates a client certificate type.
const CertificateTypeClient = CertificateType(1)

// CertificateTypeServer indicates a server certificate type.
const CertificateTypeServer = CertificateType(2)

// CertificateTypeMetrics indicates a metrics certificate type.
const CertificateTypeMetrics = CertificateType(3)

// CertificateAPITypeToDBType converts an API type to the equivalent DB type.
func CertificateAPITypeToDBType(apiType string) (CertificateType, error) {
	switch apiType {
	case api.CertificateTypeClient:
		return CertificateTypeClient, nil
	case api.CertificateTypeServer:
		return CertificateTypeServer, nil
	case api.CertificateTypeMetrics:
		return CertificateTypeMetrics, nil
	}

	return -1, fmt.Errorf("Invalid certificate type")
}

// ToAPIType returns the API equivalent type.
func (cert *Certificate) ToAPIType() string {
	switch cert.Type {
	case CertificateTypeClient:
		return api.CertificateTypeClient
	case CertificateTypeServer:
		return api.CertificateTypeServer
	case CertificateTypeMetrics:
		return api.CertificateTypeMetrics
	}

	return api.CertificateTypeUnknown
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

// GetCertificateByFingerprintPrefix gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func GetCertificateByFingerprintPrefix(ctx context.Context, tx *sql.Tx, fingerprintPrefix string) (*Certificate, error) {
	var err error
	var cert *Certificate
	sql := `
SELECT certificates.fingerprint
FROM certificates
WHERE certificates.fingerprint LIKE ?
ORDER BY certificates.fingerprint
		`

	fingerprints, err := query.SelectStrings(ctx, tx, sql, fingerprintPrefix+"%")
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
