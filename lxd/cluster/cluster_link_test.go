package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

func TestAddressSetChanged(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		updated []string
		want    bool
	}{
		{
			name:    "identical slices",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    false,
		},
		{
			name:    "same addresses different order",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.2:8443", "10.0.0.1:8443"},
			want:    false,
		},
		{
			name:    "address added",
			current: []string{"10.0.0.1:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    true,
		},
		{
			name:    "address removed",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "address replaced",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.3:8443"},
			want:    true,
		},
		{
			name:    "both empty",
			current: []string{},
			updated: []string{},
			want:    false,
		},
		{
			name:    "current empty updated non-empty",
			current: []string{},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "current non-empty updated empty",
			current: []string{"10.0.0.1:8443"},
			updated: []string{},
			want:    true,
		},
		{
			name:    "nil slices",
			current: nil,
			updated: nil,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addressSetChanged(tc.current, tc.updated)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClusterLinkConfigStorePersistsVolatileUUID(t *testing.T) {
	clusterDB, cleanup := db.NewTestCluster(t)
	defer cleanup()

	cert, key, err := shared.GenerateMemCert(false, shared.CertOptions{AddHosts: true})
	require.NoError(t, err)

	keyPair, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	block, _ := pem.Decode(cert)
	require.NotNil(t, block)

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	targetCert := shared.NewCertInfo(keyPair, x509Cert, nil)

	var clusterLinkID int64
	err = clusterDB.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		clusterLinkID, err = dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLinkRow{
			Name:        "remote",
			Description: "test cluster link",
			Type:        dbCluster.ClusterLinkType(api.ClusterLinkTypePublic),
		})
		if err != nil {
			return err
		}

		err = dbCluster.SetClusterLinkCertificate(ctx, tx.Tx(), clusterLinkID, targetCert.Fingerprint(), string(targetCert.PublicKey()))
		if err != nil {
			return err
		}

		return dbCluster.ClusterLinksConfigStore().Set(ctx, tx.Tx(), clusterLinkID, map[string]string{
			"volatile.addresses": "10.0.0.1:8443",
			"volatile.uuid":      "28e6d9fe-a94c-4b6f-a7bd-b80e265fc619",
		})
	})
	require.NoError(t, err)

	err = clusterDB.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, clusterLink, _, err := LoadClusterLinkAndCert(ctx, tx.Tx(), "remote")
		require.NoError(t, err)
		assert.Equal(t, "28e6d9fe-a94c-4b6f-a7bd-b80e265fc619", clusterLink.Config["volatile.uuid"])
		assert.Equal(t, "10.0.0.1:8443", clusterLink.Config["volatile.addresses"])
		return nil
	})
	require.NoError(t, err)
}
