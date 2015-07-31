package main

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// dbCertInfo is here to pass the certificates content
// from the database around
type dbCertInfo struct {
	ID          int
	Fingerprint string
	Type        int
	Name        string
	Certificate string
}

// dbCertsGet returns all certificates from the DB as CertBaseInfo objects.
func dbCertsGet(db *sql.DB) (certs []*dbCertInfo, err error) {
	rows, err := dbQuery(
		db,
		"SELECT id, fingerprint, type, name, certificate FROM certificates",
	)
	if err != nil {
		return certs, err
	}

	defer rows.Close()

	for rows.Next() {
		cert := new(dbCertInfo)
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

// dbCertGet gets an CertBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbCertGet(db *sql.DB, fingerprint string) (cert *dbCertInfo, err error) {
	cert = new(dbCertInfo)

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

// dbCertSave stores a CertBaseInfo object in the db,
// it will ignore the ID field from the dbCertInfo.
func dbCertSave(db *sql.DB, cert *dbCertInfo) error {
	tx, err := dbBegin(db)
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

	return txCommit(tx)
}

// dbCertDelete deletes a certificate from the db.
func dbCertDelete(db *sql.DB, fingerprint string) error {
	_, err := dbExec(
		db,
		"DELETE FROM certificates WHERE fingerprint=?",
		fingerprint,
	)

	return err
}
