package cluster_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server configuration is initially empty.
func TestConfigLoad_Initial(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := cluster.ConfigLoad(tx)

	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{}, config.Dump())
}

// If the database contains invalid keys, they are ignored.
func TestConfigLoad_IgnoreInvalidKeys(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.UpdateConfig(map[string]string{
		"foo":             "garbage",
		"core.proxy_http": "foo.bar",
	})
	require.NoError(t, err)

	config, err := cluster.ConfigLoad(tx)

	require.NoError(t, err)
	values := map[string]interface{}{"core.proxy_http": "foo.bar"}
	assert.Equal(t, values, config.Dump())
}

// Triggers can be specified to execute custom code on config key changes.
func TestConfigLoad_Triggers(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := cluster.ConfigLoad(tx)

	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{}, config.Dump())
}

// If some previously set values are missing from the ones passed to Replace(),
// they are deleted from the configuration.
func TestConfig_ReplaceDeleteValues(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := cluster.ConfigLoad(tx)
	require.NoError(t, err)

	err = config.Replace(map[string]interface{}{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)

	err = config.Replace(map[string]interface{}{})
	assert.NoError(t, err)

	assert.Equal(t, "", config.ProxyHTTP())

	values, err := tx.Config()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, values)
}

// If some previously set values are missing from the ones passed to Patch(),
// they are kept as they are.
func TestConfig_PatchKeepsValues(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := cluster.ConfigLoad(tx)
	require.NoError(t, err)

	err = config.Replace(map[string]interface{}{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)

	err = config.Patch(map[string]interface{}{})
	assert.NoError(t, err)

	assert.Equal(t, "foo.bar", config.ProxyHTTP())

	values, err := tx.Config()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"core.proxy_http": "foo.bar"}, values)
}
