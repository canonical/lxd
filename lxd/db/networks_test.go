package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkCreatePending(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.NodeAdd("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	config := map[string]string{"bridge.external_interfaces": "foo"}
	err = tx.NetworkCreatePending("buzz", "network1", config)
	require.NoError(t, err)

	networkID, err := tx.NetworkID("network1")
	require.NoError(t, err)
	assert.True(t, networkID > 0)

	config = map[string]string{"bridge.external_interfaces": "bar"}
	err = tx.NetworkCreatePending("rusp", "network1", config)
	require.NoError(t, err)

	// The initial node (whose name is 'none' by default) is missing.
	_, err = tx.NetworkNodeConfigs(networkID)
	require.EqualError(t, err, "Network not defined on nodes: none")

	config = map[string]string{"bridge.external_interfaces": "egg"}
	err = tx.NetworkCreatePending("none", "network1", config)
	require.NoError(t, err)

	// Now the storage is defined on all nodes.
	configs, err := tx.NetworkNodeConfigs(networkID)
	require.NoError(t, err)
	assert.Len(t, configs, 3)
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "foo"}, configs["buzz"])
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "bar"}, configs["rusp"])
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "egg"}, configs["none"])
}

// If an entry for the given network and node already exists, an error is
// returned.
func TestNetworksCreatePending_AlreadyDefined(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.NetworkCreatePending("buzz", "network1", map[string]string{})
	require.NoError(t, err)

	err = tx.NetworkCreatePending("buzz", "network1", map[string]string{})
	require.Equal(t, db.DbErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestNetworksCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.NetworkCreatePending("buzz", "network1", map[string]string{})
	require.Equal(t, db.NoSuchObjectError, err)
}
