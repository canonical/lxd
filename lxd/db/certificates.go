package db

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
func (n *Node) CertificatesGet() (certs []*CertInfo, err error) {
	rows, err := dbQuery(
		n.db,
		"SELECT id, fingerprint, type, name, certificate FROM certificates",
	)
	if err != nil {
		return certs, err
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

	return certs, nil
}

// CertificateGet gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (n *Node) CertificateGet(fingerprint string) (cert *CertInfo, err error) {
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

	if err = dbQueryRowScan(n.db, query, inargs, outfmt); err != nil {
		return nil, err
	}

	return cert, err
}

// CertSave stores a CertBaseInfo object in the db,
// it will ignore the ID field from the CertInfo.
func (n *Node) CertSave(cert *CertInfo) error {
	tx, err := begin(n.db)
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
			INSERT INTO certificates (
				fingerprint,
				type,
				name,
				certificate
			) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		tx.Rollback()
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
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// CertDelete deletes a certificate from the db.
func (n *Node) CertDelete(fingerprint string) error {
	_, err := exec(n.db, "DELETE FROM certificates WHERE fingerprint=?", fingerprint)
	if err != nil {
		return err
	}

	return nil
}

func (n *Node) CertUpdate(fingerprint string, certName string, certType int) error {
	tx, err := begin(n.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE certificates SET name=?, type=? WHERE fingerprint=?", certName, certType, fingerprint)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = TxCommit(tx)

	return err
}
