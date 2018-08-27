package db

import (
	"database/sql"
)

// CertInfo is here to pass the certificates content
// from the database around
type CertInfo struct {
	ID          int
	Fingerprint string
	Type        int
	Name        string
	Certificate string
}

// CertificatesGet returns all certificates from the DB as CertBaseInfo objects.
func (c *Cluster) CertificatesGet() (certs []*CertInfo, err error) {
	err = c.Transaction(func(tx *ClusterTx) error {
		rows, err := tx.tx.Query(
			"SELECT id, fingerprint, type, name, certificate FROM certificates",
		)
		if err != nil {
			return err
		}

		defer rows.Close()

		for rows.Next() {
			cert := new(CertInfo)
			rows.Scan(
				&cert.ID,
				&cert.Fingerprint,
				&cert.Type,
				&cert.Name,
				&cert.Certificate,
			)
			certs = append(certs, cert)
		}

		return rows.Err()
	})
	if err != nil {
		return certs, err
	}

	return certs, nil
}

// CertificateGet gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (c *Cluster) CertificateGet(fingerprint string) (cert *CertInfo, err error) {
	cert = new(CertInfo)

	inargs := []interface{}{fingerprint + "%"}
	outfmt := []interface{}{
		&cert.ID,
		&cert.Fingerprint,
		&cert.Type,
		&cert.Name,
		&cert.Certificate,
	}

	query := `
		SELECT
			id, fingerprint, type, name, certificate
		FROM
			certificates
		WHERE fingerprint LIKE ?`

	if err = dbQueryRowScan(c.db, query, inargs, outfmt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoSuchObject
		}

		return nil, err
	}

	return cert, err
}

// CertSave stores a CertBaseInfo object in the db,
// it will ignore the ID field from the CertInfo.
func (c *Cluster) CertSave(cert *CertInfo) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		stmt, err := tx.tx.Prepare(`
			INSERT INTO certificates (
				fingerprint,
				type,
				name,
				certificate
			) VALUES (?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		_, err = stmt.Exec(
			cert.Fingerprint,
			cert.Type,
			cert.Name,
			cert.Certificate,
		)
		if err != nil {
			return err
		}
		return nil
	})
	return err
}

// CertDelete deletes a certificate from the db.
func (c *Cluster) CertDelete(fingerprint string) error {
	err := exec(c.db, "DELETE FROM certificates WHERE fingerprint=?", fingerprint)
	if err != nil {
		return err
	}

	return nil
}

// CertUpdate updates the certificate with the given fingerprint.
func (c *Cluster) CertUpdate(fingerprint string, certName string, certType int) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE certificates SET name=?, type=? WHERE fingerprint=?", certName, certType, fingerprint)
		return err
	})
	return err
}
