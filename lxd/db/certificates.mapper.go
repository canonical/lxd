//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// The code below was generated by lxd-generate - DO NOT EDIT!

import (
	"database/sql"
	"fmt"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

var _ = api.ServerEnvironment{}

var certificateObjects = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  ORDER BY certificates.fingerprint
`)
var certificateObjectsByFingerprint = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.fingerprint LIKE ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByName = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.name = ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByFingerprintAndName = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.fingerprint LIKE ? AND certificates.name = ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByType = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.type = ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByFingerprintAndType = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.fingerprint LIKE ? AND certificates.type = ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByNameAndType = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.name = ? AND certificates.type = ? ORDER BY certificates.fingerprint
`)
var certificateObjectsByFingerprintAndNameAndType = cluster.RegisterStmt(`
SELECT certificates.id, certificates.fingerprint, certificates.type, certificates.name, certificates.certificate, certificates.restricted
  FROM certificates
  WHERE certificates.fingerprint LIKE ? AND certificates.name = ? AND certificates.type = ? ORDER BY certificates.fingerprint
`)

var certificateProjectsRef = cluster.RegisterStmt(`
SELECT fingerprint, value FROM certificates_projects_ref ORDER BY fingerprint
`)

var certificateProjectsRefByFingerprint = cluster.RegisterStmt(`
SELECT fingerprint, value FROM certificates_projects_ref WHERE fingerprint = ? ORDER BY fingerprint
`)

var certificateID = cluster.RegisterStmt(`
SELECT certificates.id FROM certificates
  WHERE certificates.fingerprint = ?
`)

var certificateCreate = cluster.RegisterStmt(`
INSERT INTO certificates (fingerprint, type, name, certificate, restricted)
  VALUES (?, ?, ?, ?, ?)
`)

var certificateDeleteByFingerprint = cluster.RegisterStmt(`
DELETE FROM certificates WHERE fingerprint = ?
`)

var certificateDeleteByNameAndType = cluster.RegisterStmt(`
DELETE FROM certificates WHERE name = ? AND type = ?
`)

var certificateUpdate = cluster.RegisterStmt(`
UPDATE certificates
  SET fingerprint = ?, type = ?, name = ?, certificate = ?, restricted = ?
 WHERE id = ?
`)

// GetCertificates returns all available certificates.
func (c *ClusterTx) GetCertificates(filter CertificateFilter) ([]Certificate, error) {
	// Result slice.
	objects := make([]Certificate, 0)

	// Check which filter criteria are active.
	criteria := map[string]interface{}{}
	if filter.Fingerprint != "" {
		criteria["Fingerprint"] = filter.Fingerprint
	}
	if filter.Name != "" {
		criteria["Name"] = filter.Name
	}
	if filter.Type != nil {
		criteria["Type"] = filter.Type
	}

	// Pick the prepared statement and arguments to use based on active criteria.
	var stmt *sql.Stmt
	var args []interface{}

	if criteria["Fingerprint"] != nil && criteria["Name"] != nil && criteria["Type"] != nil {
		stmt = c.stmt(certificateObjectsByFingerprintAndNameAndType)
		args = []interface{}{
			filter.Fingerprint,
			filter.Name,
			filter.Type,
		}
	} else if criteria["Name"] != nil && criteria["Type"] != nil {
		stmt = c.stmt(certificateObjectsByNameAndType)
		args = []interface{}{
			filter.Name,
			filter.Type,
		}
	} else if criteria["Fingerprint"] != nil && criteria["Type"] != nil {
		stmt = c.stmt(certificateObjectsByFingerprintAndType)
		args = []interface{}{
			filter.Fingerprint,
			filter.Type,
		}
	} else if criteria["Fingerprint"] != nil && criteria["Name"] != nil {
		stmt = c.stmt(certificateObjectsByFingerprintAndName)
		args = []interface{}{
			filter.Fingerprint,
			filter.Name,
		}
	} else if criteria["Type"] != nil {
		stmt = c.stmt(certificateObjectsByType)
		args = []interface{}{
			filter.Type,
		}
	} else if criteria["Name"] != nil {
		stmt = c.stmt(certificateObjectsByName)
		args = []interface{}{
			filter.Name,
		}
	} else if criteria["Fingerprint"] != nil {
		stmt = c.stmt(certificateObjectsByFingerprint)
		args = []interface{}{
			filter.Fingerprint,
		}
	} else {
		stmt = c.stmt(certificateObjects)
		args = []interface{}{}
	}

	// Dest function for scanning a row.
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

	// Select.
	err := query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch certificates")
	}

	// Fill field Projects.
	projectsObjects, err := c.CertificateProjectsRef(filter)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch field Projects")
	}

	for i := range objects {
		value := projectsObjects[objects[i].Fingerprint]
		if value == nil {
			value = []string{}
		}
		objects[i].Projects = value
	}

	return objects, nil
}

// GetCertificate returns the certificate with the given key.
func (c *ClusterTx) GetCertificate(fingerprint string) (*Certificate, error) {
	filter := CertificateFilter{}
	filter.Fingerprint = fingerprint

	objects, err := c.GetCertificates(filter)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch Certificate")
	}

	switch len(objects) {
	case 0:
		return nil, ErrNoSuchObject
	case 1:
		return &objects[0], nil
	default:
		return nil, fmt.Errorf("More than one certificate matches")
	}
}

// GetCertificateID return the ID of the certificate with the given key.
func (c *ClusterTx) GetCertificateID(fingerprint string) (int64, error) {
	stmt := c.stmt(certificateID)
	rows, err := stmt.Query(fingerprint)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to get certificate ID")
	}
	defer rows.Close()

	// Ensure we read one and only one row.
	if !rows.Next() {
		return -1, ErrNoSuchObject
	}
	var id int64
	err = rows.Scan(&id)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to scan ID")
	}
	if rows.Next() {
		return -1, fmt.Errorf("More than one row returned")
	}
	err = rows.Err()
	if err != nil {
		return -1, errors.Wrap(err, "Result set failure")
	}

	return id, nil
}

// CertificateExists checks if a certificate with the given key exists.
func (c *ClusterTx) CertificateExists(fingerprint string) (bool, error) {
	_, err := c.GetCertificateID(fingerprint)
	if err != nil {
		if err == ErrNoSuchObject {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// CreateCertificate adds a new certificate to the database.
func (c *ClusterTx) CreateCertificate(object Certificate) (int64, error) {
	// Check if a certificate with the same key exists.
	exists, err := c.CertificateExists(object.Fingerprint)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to check for duplicates")
	}
	if exists {
		return -1, fmt.Errorf("This certificate already exists")
	}

	args := make([]interface{}, 5)

	// Populate the statement arguments.
	args[0] = object.Fingerprint
	args[1] = object.Type
	args[2] = object.Name
	args[3] = object.Certificate
	args[4] = object.Restricted

	// Prepared statement to use.
	stmt := c.stmt(certificateCreate)

	// Execute the statement.
	result, err := stmt.Exec(args...)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to create certificate")
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, errors.Wrap(err, "Failed to fetch certificate ID")
	}

	return id, nil
}

// CertificateProjectsRef returns entities used by certificates.
func (c *ClusterTx) CertificateProjectsRef(filter CertificateFilter) (map[string][]string, error) {
	// Result slice.
	objects := make([]struct {
		Fingerprint string
		Value       string
	}, 0)

	// Check which filter criteria are active.
	criteria := map[string]interface{}{}
	if filter.Fingerprint != "" {
		criteria["Fingerprint"] = filter.Fingerprint
	}
	if filter.Name != "" {
		criteria["Name"] = filter.Name
	}

	// Pick the prepared statement and arguments to use based on active criteria.
	var stmt *sql.Stmt
	var args []interface{}

	if criteria["Fingerprint"] != nil {
		stmt = c.stmt(certificateProjectsRefByFingerprint)
		args = []interface{}{
			filter.Fingerprint,
		}
	} else {
		stmt = c.stmt(certificateProjectsRef)
		args = []interface{}{}
	}

	// Dest function for scanning a row.
	dest := func(i int) []interface{} {
		objects = append(objects, struct {
			Fingerprint string
			Value       string
		}{})
		return []interface{}{
			&objects[i].Fingerprint,
			&objects[i].Value,
		}
	}

	// Select.
	err := query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch string ref for certificates")
	}

	// Build index by primary name.
	index := map[string][]string{}

	for _, object := range objects {
		item, ok := index[object.Fingerprint]
		if !ok {
			item = []string{}
		}

		index[object.Fingerprint] = append(item, object.Value)
	}

	return index, nil
}

// DeleteCertificate deletes the certificate matching the given key parameters.
func (c *ClusterTx) DeleteCertificate(filter CertificateFilter) error {
	// Check which filter criteria are active.
	criteria := map[string]interface{}{}
	if filter.Fingerprint != "" {
		criteria["Fingerprint"] = filter.Fingerprint
	}
	if filter.Name != "" {
		criteria["Name"] = filter.Name
	}
	if filter.Type != nil {
		criteria["Type"] = filter.Type
	}

	// Pick the prepared statement and arguments to use based on active criteria.
	var stmt *sql.Stmt
	var args []interface{}

	if criteria["Name"] != nil && criteria["Type"] != nil {
		stmt = c.stmt(certificateDeleteByNameAndType)
		args = []interface{}{
			filter.Name,
			filter.Type,
		}
	} else if criteria["Fingerprint"] != nil {
		stmt = c.stmt(certificateDeleteByFingerprint)
		args = []interface{}{
			filter.Fingerprint,
		}
	} else {
		return fmt.Errorf("No valid filter for certificate delete")
	}
	result, err := stmt.Exec(args...)
	if err != nil {
		return errors.Wrap(err, "Delete certificate")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}
	if n != 1 {
		return fmt.Errorf("Query deleted %d rows instead of 1", n)
	}

	return nil
}

// DeleteCertificates deletes the certificate matching the given key parameters.
func (c *ClusterTx) DeleteCertificates(filter CertificateFilter) error {
	// Check which filter criteria are active.
	criteria := map[string]interface{}{}
	if filter.Fingerprint != "" {
		criteria["Fingerprint"] = filter.Fingerprint
	}
	if filter.Name != "" {
		criteria["Name"] = filter.Name
	}
	if filter.Type != nil {
		criteria["Type"] = filter.Type
	}

	// Pick the prepared statement and arguments to use based on active criteria.
	var stmt *sql.Stmt
	var args []interface{}

	if criteria["Name"] != nil && criteria["Type"] != nil {
		stmt = c.stmt(certificateDeleteByNameAndType)
		args = []interface{}{
			filter.Name,
			filter.Type,
		}
	} else if criteria["Fingerprint"] != nil {
		stmt = c.stmt(certificateDeleteByFingerprint)
		args = []interface{}{
			filter.Fingerprint,
		}
	} else {
		return fmt.Errorf("No valid filter for certificate delete")
	}
	result, err := stmt.Exec(args...)
	if err != nil {
		return errors.Wrap(err, "Delete certificate")
	}

	_, err = result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}

	return nil
}

// UpdateCertificate updates the certificate matching the given key parameters.
func (c *ClusterTx) UpdateCertificate(fingerprint string, object Certificate) error {
	id, err := c.GetCertificateID(fingerprint)
	if err != nil {
		return errors.Wrap(err, "Get certificate")
	}

	stmt := c.stmt(certificateUpdate)
	result, err := stmt.Exec(object.Fingerprint, object.Type, object.Name, object.Certificate, object.Restricted, id)
	if err != nil {
		return errors.Wrap(err, "Update certificate")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}
	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}
