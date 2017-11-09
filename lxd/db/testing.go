package db

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// NewTestNode creates a new Node for testing purposes, along with a function
// that can be used to clean it up when done.
func NewTestNode(t *testing.T) (*Node, func()) {
	dir, err := ioutil.TempDir("", "lxd-db-test-node-")
	require.NoError(t, err)

	db, err := OpenNode(dir, nil, nil)
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, db.Close())
		require.NoError(t, os.RemoveAll(dir))
	}

	return db, cleanup
}

// NewTestNodeTx returns a fresh NodeTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestNodeTx(t *testing.T) (*NodeTx, func()) {
	node, nodeCleanup := NewTestNode(t)

	var err error

	nodeTx := &NodeTx{}
	nodeTx.tx, err = node.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, nodeTx.tx.Commit())
		nodeCleanup()
	}

	return nodeTx, cleanup
}
