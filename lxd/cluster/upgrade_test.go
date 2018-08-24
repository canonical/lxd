package cluster_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

// A node can unblock other nodes that were waiting for a cluster upgrade to
// complete.
func TestNotifyUpgradeCompleted(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	gateway0 := f.Bootstrap()
	gateway1 := f.Grow()

	state0 := f.State(gateway0)

	cert0 := gateway0.Cert()
	err := cluster.NotifyUpgradeCompleted(state0, cert0)
	require.NoError(t, err)

	gateway1.WaitUpgradeNotification()
}

// The task function checks if the node is out of date and runs whatever is in
// LXD_CLUSTER_UPDATE if so.
func TestKeepUpdated_Upgrade(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	// Create a stub upgrade script that just touches a stamp file.
	stamp := filepath.Join(dir, "stamp")
	script := filepath.Join(dir, "cluster-upgrade")
	data := []byte(fmt.Sprintf("#!/bin/sh\ntouch %s\n", stamp))
	err = ioutil.WriteFile(script, data, 0755)
	require.NoError(t, err)

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	state.Node.Transaction(func(tx *db.NodeTx) error {
		nodes := []db.RaftNode{
			{ID: 1, Address: "0.0.0.0:666"},
			{ID: 2, Address: "1.2.3.4:666"},
		}
		err := tx.RaftNodesReplace(nodes)
		require.NoError(t, err)
		return nil
	})

	state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		id, err := tx.NodeAdd("buzz", "1.2.3.4:666")
		require.NoError(t, err)

		node, err := tx.NodeByName("buzz")
		require.NoError(t, err)

		version := node.Version()
		version[0]++

		err = tx.NodeUpdateVersion(id, version)
		require.NoError(t, err)

		return nil
	})

	os.Setenv("LXD_CLUSTER_UPDATE", script)
	defer os.Unsetenv("LXD_CLUSTER_UPDATE")

	f, _ := cluster.KeepUpdated(state)
	f(context.Background())

	_, err = os.Stat(stamp)
	require.NoError(t, err)
}

// If the node is up-to-date, nothing is done.
func TestKeepUpdated_NothingToDo(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	// Create a stub upgrade script that just touches a stamp file.
	stamp := filepath.Join(dir, "stamp")
	script := filepath.Join(dir, "cluster-upgrade")
	data := []byte(fmt.Sprintf("#!/bin/sh\ntouch %s\n", stamp))
	err = ioutil.WriteFile(script, data, 0755)
	require.NoError(t, err)

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	os.Setenv("LXD_CLUSTER_UPDATE", script)
	defer os.Unsetenv("LXD_CLUSTER_UPDATE")

	f, _ := cluster.KeepUpdated(state)
	f(context.Background())

	_, err = os.Stat(stamp)
	require.True(t, os.IsNotExist(err))
}
