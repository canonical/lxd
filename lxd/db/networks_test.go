// +build linux,cgo,!agent

package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The GetNetworksLocalConfigs method returns only node-specific config values.
func TestGetNetworksLocalConfigs(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	_, err := cluster.CreateNetwork("lxdbr0", "", db.NetworkTypeBridge, map[string]string{
		"dns.mode":                   "none",
		"bridge.external_interfaces": "vlan0",
	})
	require.NoError(t, err)

	var config map[string]map[string]string

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		config, err = tx.GetNetworksLocalConfig()
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, config, map[string]map[string]string{
		"lxdbr0": {"bridge.external_interfaces": "vlan0"},
	})
}

func TestCreatePendingNetwork(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.CreateNode("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	config := map[string]string{"bridge.external_interfaces": "foo"}
	err = tx.CreatePendingNetwork("buzz", "network1", db.NetworkTypeBridge, config)
	require.NoError(t, err)

	networkID, err := tx.GetNetworkID("network1")
	require.NoError(t, err)
	assert.True(t, networkID > 0)

	config = map[string]string{"bridge.external_interfaces": "bar"}
	err = tx.CreatePendingNetwork("rusp", "network1", db.NetworkTypeBridge, config)
	require.NoError(t, err)

	// The initial node (whose name is 'none' by default) is missing.
	_, err = tx.NetworkNodeConfigs(networkID)
	require.EqualError(t, err, "Network not defined on nodes: none")

	config = map[string]string{"bridge.external_interfaces": "egg"}
	err = tx.CreatePendingNetwork("none", "network1", db.NetworkTypeBridge, config)
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

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.CreatePendingNetwork("buzz", "network1", db.NetworkTypeBridge, map[string]string{})
	require.NoError(t, err)

	err = tx.CreatePendingNetwork("buzz", "network1", db.NetworkTypeBridge, map[string]string{})
	require.Equal(t, db.ErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestNetworksCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.CreatePendingNetwork("buzz", "network1", db.NetworkTypeBridge, map[string]string{})
	require.Equal(t, db.ErrNoSuchObject, err)
}
