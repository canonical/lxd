package state

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
)

// NewTestState returns a State object initialized with testable instances of
// the node/cluster databases and of the OS facade.
//
// Return the newly created State object, along with a function that can be
// used for cleaning it up.
func NewTestState(t *testing.T) (*State, func()) {
	node, nodeCleanup := db.NewTestNode(t)
	cluster, clusterCleanup := db.NewTestCluster(t)
	os, osCleanup := sys.NewTestOS(t)

	cleanup := func() {
		nodeCleanup()
		clusterCleanup()
		osCleanup()
	}

	state := NewState(node, cluster, nil, os, nil)

	return state, cleanup
}
