package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Node-local configuration values are initially empty.
func TestTx_Config(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()
	values, err := tx.Config()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, values)
	assert.NoError(t, err)
}

// Node-local configuration values can be updated with UpdateConfig.
func TestTx_UpdateConfig(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.UpdateConfig(map[string]string{"foo": "x", "bar": "y"})
	require.NoError(t, err)

	values, err := tx.Config()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "x", "bar": "y"}, values)
}

// Keys that are associated with empty strings are deleted.
func TestTx_UpdateConfigUnsetKeys(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.UpdateConfig(map[string]string{"foo": "x", "bar": "y"})
	require.NoError(t, err)
	err = tx.UpdateConfig(map[string]string{"foo": "x", "bar": ""})
	require.NoError(t, err)

	values, err := tx.Config()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "x"}, values)
}
