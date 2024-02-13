package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// updateIdentityCache reads all identities from the database and sets them in the identity.Cache.
// The certificates in the local database are replaced with identities in the cluster database that
// are of type api.IdentityTypeCertificateServer. This ensures that this cluster member is able to
// trust other cluster members on restart.
func updateIdentityCache(d *Daemon) {
	s := d.State()

	logger.Debug("Refreshing identity cache")

	var identities []cluster.Identity
	projects := make(map[int][]string)
	var err error
	err = s.DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		identities, err = cluster.GetIdentitys(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, identity := range identities {
			identityProjects, err := cluster.GetIdentityProjects(ctx, tx.Tx(), identity.ID)
			if err != nil {
				return err
			}

			for _, p := range identityProjects {
				projects[identity.ID] = append(projects[identity.ID], p.Name)
			}
		}
		return nil
	})
	if err != nil {
		logger.Warn("Failed reading certificates from global database", logger.Ctx{"err": err})
		return
	}

	identityCacheEntries := make([]identity.CacheEntry, 0, len(identities))
	var localServerCerts []cluster.Certificate
	for _, id := range identities {
		cacheEntry := identity.CacheEntry{
			Identifier:           id.Identifier,
			Name:                 id.Name,
			AuthenticationMethod: string(id.AuthMethod),
			IdentityType:         string(id.Type),
			Projects:             projects[id.ID],
		}

		if cacheEntry.AuthenticationMethod == api.AuthenticationMethodTLS {
			cert, err := id.X509()
			if err != nil {
				logger.Warn("Failed to extract x509 certificate from TLS identity metadata", logger.Ctx{"error": err})
				continue
			}

			cacheEntry.Certificate = cert
		} else if cacheEntry.AuthenticationMethod == api.AuthenticationMethodOIDC {
			subject, err := id.Subject()
			if err != nil {
				logger.Warn("Failed to extract OIDC subject from OIDC identity metadata", logger.Ctx{"error": err})
				continue
			}

			cacheEntry.Subject = subject
		}

		identityCacheEntries = append(identityCacheEntries, cacheEntry)

		// Add server certs to list of certificates to store in local database to allow cluster restart.
		if id.Type == api.IdentityTypeCertificateServer {
			cert, err := id.ToCertificate()
			if err != nil {
				logger.Warn("Failed to convert TLS identity to server certificate", logger.Ctx{"error": err})
			}

			localServerCerts = append(localServerCerts, *cert)
		}
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = s.DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localServerCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", logger.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	err = d.identityCache.ReplaceAll(identityCacheEntries)
	if err != nil {
		logger.Warn("Failed to update identity cache", logger.Ctx{"error": err})
	}
}

// updateIdentityCacheFromLocal loads trusted server certificates from local database into the identity cache.
func updateIdentityCacheFromLocal(d *Daemon) error {
	logger.Debug("Refreshing identity cache with local trusted certificates")

	var localServerCerts []cluster.Certificate
	var err error

	err = d.State().DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		localServerCerts, err = tx.GetCertificates(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed reading certificates from local database: %w", err)
	}

	var identityCacheEntries []identity.CacheEntry
	for _, dbCert := range localServerCerts {
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

		id, err := dbCert.ToIdentity()
		if err != nil {
			logger.Warn("Failed to convert node certificate into identity entry", logger.Ctx{"error": err})
			continue
		}

		identityCacheEntries = append(identityCacheEntries, identity.CacheEntry{
			Identifier:           id.Identifier,
			AuthenticationMethod: string(id.AuthMethod),
			IdentityType:         string(id.Type),
			Certificate:          cert,
		})
	}

	err = d.identityCache.ReplaceAll(identityCacheEntries)
	if err != nil {
		return fmt.Errorf("Failed to update identity cache from local trust store: %w", err)
	}

	return nil
}
