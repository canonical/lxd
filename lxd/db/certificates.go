//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"github.com/lxc/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t certificates.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e certificate objects
//go:generate mapper stmt -p db -e certificate objects-by-Fingerprint
//go:generate mapper stmt -p db -e certificate projects-ref
//go:generate mapper stmt -p db -e certificate projects-ref-by-Fingerprint
//go:generate mapper stmt -p db -e certificate id
//go:generate mapper stmt -p db -e certificate create struct=Certificate
//go:generate mapper stmt -p db -e certificate create-projects-ref
//go:generate mapper stmt -p db -e certificate delete
//go:generate mapper stmt -p db -e certificate update struct=Certificate
//
//go:generate mapper method -p db -e certificate List
//go:generate mapper method -p db -e certificate Get
//go:generate mapper method -p db -e certificate ID struct=Certificate
//go:generate mapper method -p db -e certificate Exists struct=Certificate
//go:generate mapper method -p db -e certificate Create struct=Certificate
//go:generate mapper method -p db -e certificate ProjectsRef
//go:generate mapper method -p db -e certificate Delete
//go:generate mapper method -p db -e certificate Update struct=Certificate

// Certificate is here to pass the certificates content
// from the database around
type Certificate struct {
	ID          int
	Fingerprint string `db:"primary=yes&comparison=like"`
	Type        int
	Name        string
	Certificate string
	Restricted  bool
	Projects    []string
}

// ToAPI converts the database Certificate struct to an api.Certificate entry.
func (cert *Certificate) ToAPI() api.Certificate {
	resp := api.Certificate{}
	resp.Fingerprint = cert.Fingerprint
	resp.Certificate = cert.Certificate
	resp.Name = cert.Name
	resp.Restricted = cert.Restricted
	resp.Projects = cert.Projects
	if cert.Type == 1 {
		resp.Type = "client"
	} else {
		resp.Type = "unknown"
	}

	return resp
}

// UpdateCertificateProjects updates the list of projects on a certificate.
func (c *ClusterTx) UpdateCertificateProjects(id int, projects []string) error {
	// Clear all projects from the restrictions.
	q := "DELETE FROM certificates_projects WHERE certificate_id=?"
	_, err := c.tx.Exec(q, id)
	if err != nil {
		return err
	}

	// Add the new restrictions.
	for _, name := range projects {
		projID, err := c.GetProjectID(name)
		if err != nil {
			return err
		}

		q := "INSERT INTO certificates_projects (certificate_id, project_id) VALUES (?, ?)"
		_, err = c.tx.Exec(q, id, projID)
		if err != nil {
			return err
		}
	}

	return nil
}

// CertificateFilter can be used to filter results yielded by GetCertInfos
type CertificateFilter struct {
	Fingerprint string // Matched with LIKE
}

// GetCertificate gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one certificate with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (c *Cluster) GetCertificate(fingerprint string) (*Certificate, error) {
	var err error
	var cert *Certificate
	err = c.Transaction(func(tx *ClusterTx) error {
		cert, err = tx.GetCertificate(fingerprint + "%")
		if err != nil {
			return err
		}

		cert, err = tx.GetCertificate(cert.Fingerprint)
		return err
	})
	if err != nil {
		return nil, err
	}

	return cert, nil
}

// CreateCertificate stores a CertInfo object in the db, it will ignore the ID
// field from the CertInfo.
func (c *Cluster) CreateCertificate(cert Certificate) (int64, error) {
	var id int64
	var err error
	err = c.Transaction(func(tx *ClusterTx) error {
		id, err = tx.CreateCertificate(cert)
		return err
	})
	return id, err
}

// DeleteCertificate deletes a certificate from the db.
func (c *Cluster) DeleteCertificate(fingerprint string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.DeleteCertificate(fingerprint)
	})
	return err
}

// UpdateCertificate updates a certificate in the db.
func (c *Cluster) UpdateCertificate(fingerprint string, cert Certificate) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.UpdateCertificate(fingerprint, cert)
	})
	return err
}

// UpdateCertificateProjects updates the list of projects on a certificate.
func (c *Cluster) UpdateCertificateProjects(id int, projects []string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.UpdateCertificateProjects(id, projects)
	})
	return err
}
