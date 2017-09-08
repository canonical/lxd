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
	dir, err := ioutil.TempDir("", "lxd-db-test")
	require.NoError(t, err)

	db, err := New(dir, nil, nil)
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, db.Close())
		require.NoError(t, os.RemoveAll(dir))
	}

	return db, cleanup
}
