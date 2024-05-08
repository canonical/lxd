package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
)

// The server configuration is initially empty.
func TestConfigLoad_Initial(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	dump, err := config.Dump()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, dump)

	offlineThreshold, err := config.OfflineThreshold()
	require.NoError(t, err)
	assert.Equal(t, float64(20), offlineThreshold.Seconds())
}

// If the database contains invalid keys, they are ignored.
func TestConfigLoad_IgnoreInvalidKeys(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.UpdateClusterConfig(map[string]string{
		"foo":             "garbage",
		"core.proxy_http": "foo.bar",
	})
	require.NoError(t, err)

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	values := map[string]string{"core.proxy_http": "foo.bar"}
	dump, err := config.Dump()
	require.NoError(t, err)
	assert.Equal(t, values, dump)
}

// Triggers can be specified to execute custom code on config key changes.
func TestConfigLoad_Triggers(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	dump, err := config.Dump()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, dump)
}

// Offline threshold must be greater than the heartbeat interval.
func TestConfigLoad_OfflineThresholdValidator(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(map[string]string{"cluster.offline_threshold": "2"})
	require.EqualError(t, err, "Cannot set 'cluster.offline_threshold' to '2': Value must be greater than '10'")
}

// Max number of voters must be odd.
func TestConfigLoad_MaxVotersValidator(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(map[string]string{"cluster.max_voters": "4"})
	require.EqualError(t, err, "Cannot set 'cluster.max_voters' to '4': Value must be an odd number equal to or higher than 3")
}

// If some previously set values are missing from the ones passed to Replace(),
// they are deleted from the configuration.
func TestConfig_ReplaceDeleteValues(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	changed, err := config.Replace(map[string]string{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"core.proxy_http": "foo.bar"}, changed)

	_, err = config.Replace(map[string]string{})
	assert.NoError(t, err)

	proxy, err := config.ProxyHTTP()
	assert.NoError(t, err)
	assert.Equal(t, "", proxy)

	values, err := tx.Config(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, values)
}

// If some previously set values are missing from the ones passed to Patch(),
// they are kept as they are.
func TestConfig_PatchKeepsValues(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Replace(map[string]string{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)

	_, err = config.Patch(map[string]string{})
	assert.NoError(t, err)

	proxy, err := config.ProxyHTTP()
	assert.NoError(t, err)
	assert.Equal(t, "foo.bar", proxy)

	values, err := tx.Config(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"core.proxy_http": "foo.bar"}, values)
}
