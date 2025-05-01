package cluster_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

func TestClusterState(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	state.ServerCert = func() *shared.CertInfo { return cert }

	f := notifyFixtures{t: t, state: state}
	cleanupF := f.Nodes(cert, 3)
	defer cleanupF()

	// Populate state.LocalConfig after nodes created above.
	var err error
	var nodeConfig *node.Config
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	state.LocalConfig = nodeConfig

	states, err := cluster.ClusterState(state, cert)
	require.NoError(t, err)

	assert.Len(t, states, 3)

	for clusterMemberName, state := range states {
		// Local cluster member
		if clusterMemberName == "0" {
			assert.Greater(t, state.SysInfo.LogicalCPUs, uint64(0))
			continue
		}

		assert.Equal(t, uint64(24), state.SysInfo.LogicalCPUs)
	}

	var members []db.NodeInfo
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err = tx.GetNodes(ctx)
		return err
	})
	require.NoError(t, err)

	for i, memberInfo := range members {
		if memberInfo.Name == "0" {
			members[i] = members[len(members)-1]
			members = members[:len(members)-1]
			break
		}
	}

	states, err = cluster.ClusterState(state, cert, members...)
	require.NoError(t, err)

	assert.Len(t, states, 2)
	for _, state := range states {
		assert.Equal(t, uint64(24), state.SysInfo.LogicalCPUs)
	}
}
