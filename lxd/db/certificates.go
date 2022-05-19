//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
)

// UpdateCertificate updates a certificate in the db.
func (db *DB) UpdateCertificate(ctx context.Context, fingerprint string, cert cluster.Certificate, projectNames []string) error {
	err := db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		id, err := cluster.GetCertificateID(ctx, tx.Tx(), fingerprint)
		if err != nil {
			return err
		}

		err = cluster.UpdateCertificate(ctx, tx.Tx(), fingerprint, cert)
		if err != nil {
			return err
		}

		return cluster.UpdateCertificateProjects(ctx, tx.Tx(), int(id), projectNames)
	})

	return err
}

// GetCertificates returns all available local certificates.
func (n *NodeTx) GetCertificates() ([]cluster.Certificate, error) {
	dbCerts := []struct {
		fingerprint string
		certType    cluster.CertificateType
		name        string
		certificate string
	}{}
	dest := func(i int) []any {
		dbCerts = append(dbCerts, struct {
			fingerprint string
			certType    cluster.CertificateType
			name        string
			certificate string
		}{})
		return []any{&dbCerts[i].fingerprint, &dbCerts[i].certType, &dbCerts[i].name, &dbCerts[i].certificate}
	}

	stmt, err := n.tx.Prepare("SELECT fingerprint, type, name, certificate FROM certificates")
	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return nil, err
	}

	certs := make([]cluster.Certificate, 0, len(dbCerts))
	for _, dbCert := range dbCerts {
		certs = append(certs, cluster.Certificate{
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
func (n *NodeTx) ReplaceCertificates(certs []cluster.Certificate) error {
	_, err := n.tx.Exec("DELETE FROM certificates")
	if err != nil {
		return err
	}

	stmt, err := n.tx.Prepare("INSERT INTO certificates (fingerprint, type, name, certificate) VALUES(?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, cert := range certs {
		_, err = stmt.Exec(cert.Fingerprint, cert.Type, cert.Name, cert.Certificate)
		if err != nil {
			return err
		}
	}

	return nil
}
