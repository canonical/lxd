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
	client := f.ClientUnix(daemon).ClusterTargetNode("rusp-0")

	networkPost := api.NetworksPost{
		Name: "mynetwork",
	}

	err := client.CreateNetwork(networkPost)
	require.NoError(t, err)

	network, _, err := client.GetNetwork("mynetwork")
	require.NoError(t, err)

	assert.Equal(t, "PENDING", network.State)
	assert.Equal(t, []string{"rusp-0"}, network.Nodes)
}
