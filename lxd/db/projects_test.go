//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/require"
)

func TestProjectsList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.GetProject("default")
	require.NoError(t, err)

}
