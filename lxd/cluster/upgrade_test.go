package cluster_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/stretchr/testify/require"
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
