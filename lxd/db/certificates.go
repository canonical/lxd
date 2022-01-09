//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t certificates.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e certificate objects
//go:generate mapper stmt -p db -e certificate objects-by-Fingerprint
//go:generate mapper stmt -p db -e certificate id
//go:generate mapper stmt -p db -e certificate create struct=Certificate
//go:generate mapper stmt -p db -e certificate delete-by-Fingerprint
//go:generate mapper stmt -p db -e certificate delete-by-Name-and-Type
//go:generate mapper stmt -p db -e certificate update struct=Certificate
//
//go:generate mapper method -p db -e certificate GetMany
//go:generate mapper method -p db -e certificate GetOne
//go:generate mapper method -p db -e certificate ID struct=Certificate
//go:generate mapper method -p db -e certificate Exists struct=Certificate
//go:generate mapper method -p db -e certificate Create struct=Certificate
//go:generate mapper method -p db -e certificate DeleteOne-by-Fingerprint
//go:generate mapper method -p db -e certificate DeleteMany-by-Name-and-Type
//go:generate mapper method -p db -e certificate Update struct=Certificate

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

// Certificate is here to pass the certificates content from the database around.
type Certificate struct {
	ID          int
	Fingerprint string `db:"primary=yes"`
	Type        CertificateType
	Name        string
	Certificate string
	Restricted  bool
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
func (cert *Certificate) ToAPI(tx *ClusterTx) (*api.Certificate, error) {
	resp := api.Certificate{}
	resp.Fingerprint = cert.Fingerprint
	resp.Certificate = cert.Certificate
	resp.Name = cert.Name
	resp.Restricted = cert.Restricted
	resp.Type = cert.ToAPIType()

	projects, err := tx.GetCertificateProjects(*cert)
	if err != nil {
		return nil, err
	}

	resp.Projects = make([]string, len(projects))
	for i, p := range projects {
		resp.Projects[i] = p.Name
	}

	return &resp, nil
}

// CertificateFilter specifies potential query parameter fields.
type CertificateFilter struct {
	Fingerprint *string
	Name        *string
	Type        *CertificateType
}

// GetCertificateByFingerprintPrefix gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (c *ClusterTx) GetCertificateByFingerprintPrefix(fingerprintPrefix string) (*Certificate, error) {
	var err error
	var cert *Certificate
	objects := []Certificate{}
	sql := `
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
FROM certificates
WHERE certificates.fingerprint LIKE ?
ORDER BY certificates.fingerprint
		`
	stmt, err := c.prepare(sql)
	if err != nil {
		return nil, err
	}
	dest := func(i int) []interface{} {
		objects = append(objects, Certificate{})
		return []interface{}{
			&objects[i].ID,
			&objects[i].Fingerprint,
			&objects[i].Type,
			&objects[i].Name,
			&objects[i].Certificate,
			&objects[i].Restricted,
		}
	}

	err = query.SelectObjects(stmt, dest, fingerprintPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch certificates: %w", err)
	}

	if len(objects) > 1 {
		return nil, fmt.Errorf("More than one certificate matches")
	}

	if len(objects) == 0 {
		return nil, ErrNoSuchObject
	}

	cert, err = c.GetCertificate(objects[0].Fingerprint)
	if err != nil {
		return nil, err
	}

	return cert, nil
}

// CreateCertificateWithProjects stores a CertInfo object in the db, and associates it to a list of project names.
// It will ignore the ID field from the CertInfo.
func (c *ClusterTx) CreateCertificateWithProjects(cert Certificate, projects []string) (int64, error) {
	var id int64
	var err error
	id, err = c.CreateCertificate(cert)
	if err != nil {
		return -1, err
	}

	cert.ID = int(id)

	if len(projects) > 0 {
		dbProjects := make([]Project, len(projects))
		for i, p := range projects {
			project, err := c.GetProject(p)
			if err != nil {
				return -1, err
			}

			dbProjects[i] = *project
		}

		err = c.UpdateCertificateProjects(cert, dbProjects)
		if err != nil {
			return -1, err
		}
	}

	return id, err
}

// UpdateCertificate updates a certificate in the db.
func (c *Cluster) UpdateCertificate(fingerprint string, newCert Certificate, projects []string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		// Get the ID for the certificate.
		id, err := tx.GetCertificateID(fingerprint)
		if err != nil {
			return err
		}

		err = tx.UpdateCertificate(fingerprint, newCert)
		if err != nil {
			return err
		}

		if len(projects) > 0 {
			dbProjects := make([]Project, len(projects))
			for i, p := range projects {
				project, err := tx.GetProject(p)
				if err != nil {
					return err
				}

				dbProjects[i] = *project
			}

			newCert.ID = int(id)
			return tx.UpdateCertificateProjects(newCert, dbProjects)
		}

		return nil
	})
	return err
}

// GetCertificates returns all available local certificates.
func (n *NodeTx) GetCertificates() ([]Certificate, error) {
	dbCerts := []struct {
		fingerprint string
		certType    CertificateType
		name        string
		certificate string
	}{}
	dest := func(i int) []interface{} {
		dbCerts = append(dbCerts, struct {
			fingerprint string
			certType    CertificateType
			name        string
			certificate string
		}{})
		return []interface{}{&dbCerts[i].fingerprint, &dbCerts[i].certType, &dbCerts[i].name, &dbCerts[i].certificate}
	}

	stmt, err := n.tx.Prepare("SELECT fingerprint, type, name, certificate FROM certificates")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return nil, err
	}

	certs := make([]Certificate, 0, len(dbCerts))
	for _, dbCert := range dbCerts {
		certs = append(certs, Certificate{
			Fingerprint: dbCert.fingerprint,
			Type:        dbCert.certType,
			Name:        dbCert.name,
			Certificate: dbCert.certificate,
		})
	}

	return certs, nil
}

// ReplaceCertificates removes all existing certificates from the local certificates table and replaces them with
// the ones provided.
func (n *NodeTx) ReplaceCertificates(certs []Certificate) error {
	_, err := n.tx.Exec("DELETE FROM certificates")
	if err != nil {
		return err
	}

	stmt, err := n.tx.Prepare("INSERT INTO certificates (fingerprint, type, name, certificate) VALUES(?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, cert := range certs {
		_, err = stmt.Exec(cert.Fingerprint, cert.Type, cert.Name, cert.Certificate)
		if err != nil {
			return err
		}
	}

	return nil
}
