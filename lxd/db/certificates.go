package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
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

// CertsGet returns all certificates from the DB as CertBaseInfo objects.
func CertsGet(db *sql.DB) (certs []*CertInfo, err error) {
	rows, err := dbQuery(
		db,
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

// CertGet gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func CertGet(db *sql.DB, fingerprint string) (cert *CertInfo, err error) {
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

	if err = dbQueryRowScan(db, query, inargs, outfmt); err != nil {
		return nil, err
	}

	return cert, err
}

// CertSave stores a CertBaseInfo object in the db,
// it will ignore the ID field from the CertInfo.
func CertSave(db *sql.DB, cert *CertInfo) error {
	tx, err := Begin(db)
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
func CertDelete(db *sql.DB, fingerprint string) error {
	_, err := Exec(db, "DELETE FROM certificates WHERE fingerprint=?", fingerprint)
	if err != nil {
		return err
	}

	return nil
}

func CertUpdate(db *sql.DB, fingerprint string, certName string, certType int) error {
	tx, err := Begin(db)
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
