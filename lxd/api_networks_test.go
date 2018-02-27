package main

import (
	"testing"

	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Create a new pending network using the targetNode query paramenter.
func TestNetworksCreate_TargetNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping storage-networks targetNode test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	daemon := daemons[0]
	client := f.ClientUnix(daemon).UseTarget("rusp-0")

	networkPost := api.NetworksPost{
		Name: "mynetwork",
	}

	err := client.CreateNetwork(networkPost)
	require.NoError(t, err)

	network, _, err := client.GetNetwork("mynetwork")
	require.NoError(t, err)

	assert.Equal(t, "Pending", network.Status)
	assert.Equal(t, []string{"rusp-0"}, network.Locations)

	// If a network is pending, deleting it just means removing the
	// relevant rows from the database.
	err = client.DeleteNetwork("mynetwork")
	require.NoError(t, err)

	_, _, err = client.GetNetwork("mynetwork")
	require.EqualError(t, err, "not found")
}

// An error is returned when trying to create a new network in a cluster
// where the network was not defined on any node nodes.
func TestNetworksCreate_NotDefined(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	// Trying to create the pool now results in an error, since it's not
	// defined on any node.
	networkPost := api.NetworksPost{
		Name: "mynetwork",
	}
	client := f.ClientUnix(daemons[0])
	err := client.CreateNetwork(networkPost)
	require.EqualError(t, err, "Network not pending on any node (use --target <node> first)")
}

// An error is returned when trying to create a new network in a cluster where
// the network was not defined on all nodes.
func TestNetworksCreate_MissingNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping networks targetNode test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	// Define the network on rusp-0.
	daemon := daemons[0]
	client := f.ClientUnix(daemon).UseTarget("rusp-0")

	networkPost := api.NetworksPost{
		Name: "mynetwork",
	}

	err := client.CreateNetwork(networkPost)
	require.NoError(t, err)

	// Trying to create the network now results in an error, since it's not
	// defined on all nodes.
	networkPost = api.NetworksPost{
		Name: "mynetwork",
	}
	client = f.ClientUnix(daemon)
	err = client.CreateNetwork(networkPost)
	require.EqualError(t, err, "Network not defined on nodes: buzz")
}
