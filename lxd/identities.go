package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/canonical/lxd/lxd/certificate"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func updateCertificateCache(d *Daemon) {
	s := d.State()

	logger.Debug("Refreshing trusted certificate cache")

	newCerts := map[certificate.Type]map[string]x509.Certificate{}
	newProjects := map[string][]string{}

	var certs []*api.Certificate
	var dbCerts []dbCluster.Certificate
	var localCerts []dbCluster.Certificate
	var err error
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbCerts, err = dbCluster.GetCertificates(ctx, tx.Tx())
		if err != nil {
			return err
		}

		certs = make([]*api.Certificate, len(dbCerts))
		for i, c := range dbCerts {
			certs[i], err = c.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Warn("Failed reading certificates from global database", logger.Ctx{"err": err})
		return
	}

	for i, dbCert := range dbCerts {
		_, found := newCerts[dbCert.Type]
		if !found {
			newCerts[dbCert.Type] = make(map[string]x509.Certificate)
		}

		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		newCerts[dbCert.Type][shared.CertFingerprint(cert)] = *cert

		if dbCert.Restricted {
			newProjects[shared.CertFingerprint(cert)] = certs[i].Projects
		}

		// Add server certs to list of certificates to store in local database to allow cluster restart.
		if dbCert.Type == certificate.TypeServer {
			localCerts = append(localCerts, dbCert)
		}
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", logger.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	d.clientCerts.SetCertificatesAndProjects(newCerts, newProjects)
}

// updateCertificateCacheFromLocal loads trusted server certificates from local database into memory.
func updateCertificateCacheFromLocal(d *Daemon) error {
	logger.Debug("Refreshing local trusted certificate cache")

	newCerts := map[certificate.Type]map[string]x509.Certificate{}

	var dbCerts []dbCluster.Certificate
	var err error

	err = d.State().DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		dbCerts, err = tx.GetCertificates(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed reading certificates from local database: %w", err)
	}

	for _, dbCert := range dbCerts {
		_, found := newCerts[dbCert.Type]
		if !found {
			newCerts[dbCert.Type] = make(map[string]x509.Certificate)
		}

		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		newCerts[dbCert.Type][shared.CertFingerprint(cert)] = *cert
	}

	d.clientCerts.SetCertificates(newCerts)

	return nil
}
