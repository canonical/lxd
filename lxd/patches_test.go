package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

func Test_patchSplitIdentityCertificateEntityTypes(t *testing.T) {
	// Set up test database.
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()

	var groupID int
	var certificateID int
	var identityID int
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Create a group.
		groupIDint64, err := dbCluster.CreateAuthGroup(ctx, tx.Tx(), dbCluster.AuthGroup{
			Name: "test-group",
		})
		require.NoError(t, err)
		groupID = int(groupIDint64)

		// Create a certificate
		cert, _, err := shared.GenerateMemCert(true, shared.CertOptions{})
		require.NoError(t, err)
		x509Cert, err := shared.ParseCert(cert)
		require.NoError(t, err)

		certificateIDint64, err := dbCluster.CreateCertificate(ctx, tx.Tx(), dbCluster.Certificate{
			Fingerprint: shared.CertFingerprint(x509Cert),
			Type:        certificate.TypeClient,
			Name:        "test-cert",
			Certificate: string(cert),
			Restricted:  false,
		})
		require.NoError(t, err)
		certificateID = int(certificateIDint64)

		// Create an OIDC identity
		oidcMetadata, err := json.Marshal(dbCluster.OIDCMetadata{Subject: "test-subject"})
		require.NoError(t, err)
		identityIDint64, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
			AuthMethod: api.AuthenticationMethodOIDC,
			Type:       api.IdentityTypeOIDCClient,
			Identifier: "jane.doe@example.com",
			Name:       "Jane Doe",
			Metadata:   string(oidcMetadata),
		})
		require.NoError(t, err)
		identityID = int(identityIDint64)

		// Create three permissions for the group.
		err = dbCluster.SetAuthGroupPermissions(ctx, tx.Tx(), groupID, []dbCluster.Permission{
			{
				// This permission has "identity" as the entity type but references a certificate. We expect the entity
				// type to change to "certificate" after the patch.
				Entitlement: auth.EntitlementCanView,
				EntityType:  dbCluster.EntityType(entity.TypeIdentity),
				EntityID:    certificateID,
			},
			{
				// This permission has "certificate" as the entity type. We expect that this will be replaced after the patch.
				Entitlement: auth.EntitlementCanView,
				EntityType:  dbCluster.EntityType(entity.TypeCertificate),
				EntityID:    certificateID,
			},
			{
				// This permission also has "identity" as the entity type and this references an OIDC client. The entity
				// type for this permission should not change.
				Entitlement: auth.EntitlementCanView,
				EntityType:  dbCluster.EntityType(entity.TypeIdentity),
				EntityID:    identityID,
			},
		})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	// Run the patch.
	daemonDB := &db.DB{Cluster: cluster}
	daemon := &Daemon{db: daemonDB, shutdownCtx: ctx}
	err = patchSplitIdentityCertificateEntityTypes("", daemon)
	require.NoError(t, err)

	// Get the permissions.
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		permissions, err := dbCluster.GetPermissionsByAuthGroupID(ctx, tx.Tx(), groupID)
		require.NoError(t, err)

		// The second permission should have been overwritten, so there should now only be two permissions.
		assert.Len(t, permissions, 2)

		// The first permission we created should have the entity type changed to "certificate".
		assert.Equal(t, entity.TypeCertificate, entity.Type(permissions[0].EntityType))

		// The entity type of the third permission should not have changed.
		assert.Equal(t, entity.TypeIdentity, entity.Type(permissions[1].EntityType))
		return nil
	})
	require.NoError(t, err)
}
