package config_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/shared/features"
)

// TestMain turns the failure-domain-aware placement feature preview on for every test in this
// package -- it's off by default for real installs, but TestConfigLoad_FailureDomainsValidator
// exercises cluster.failure_domains directly, which validateClusterFailureDomains rejects as an
// unknown key while the preview is off.
func TestMain(m *testing.M) {
	os.Setenv(features.EnvVar, string(features.FailureDomainPlacement))

	err := features.LoadFromEnv(features.EnvVar)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// The server configuration is initially empty.
func TestConfigLoad_Initial(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	clusterUUID := config.ClusterUUID()
	uuidv7, err := uuid.Parse(clusterUUID)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), uuidv7.Version())
	assert.Equal(t, map[string]string{
		"volatile.uuid": clusterUUID,
	}, config.Dump())

	assert.Equal(t, float64(20), config.OfflineThreshold().Seconds())
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
	values := map[string]string{"core.proxy_http": "foo.bar", "volatile.uuid": config.ClusterUUID()}
	assert.Equal(t, values, config.Dump())
}

// Triggers can be specified to execute custom code on config key changes.
func TestConfigLoad_Triggers(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"volatile.uuid": config.ClusterUUID(),
	}, config.Dump())
}

func TestConfig_DumpPublicUnauthenticated(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	publicConfig := config.DumpPublic(false)
	assert.NotContains(t, publicConfig, "volatile.uuid")
}

func TestConfig_DumpPublicAuthenticated(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	publicConfig := config.DumpPublic(true)
	assert.Equal(t, config.ClusterUUID(), publicConfig["volatile.uuid"])
}

// Offline threshold must be greater than the heartbeat interval.
func TestConfigLoad_OfflineThresholdValidator(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(context.Background(), tx, map[string]string{"cluster.offline_threshold": "2"})
	require.EqualError(t, err, `Cannot set "cluster.offline_threshold" to "2": Value must be greater than 10`)
}

// Max number of voters must be odd.
func TestConfigLoad_MaxVotersValidator(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(context.Background(), tx, map[string]string{"cluster.max_voters": "4"})
	require.EqualError(t, err, `Cannot set "cluster.max_voters" to "4": Value must be an odd number equal to or higher than 3`)
}

// cluster.failure_domains is validated against the known-domains registry: unknown names are
// rejected, and names already assigned to a cluster member are accepted.
func TestConfigLoad_FailureDomainsValidator(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(context.Background(), tx, map[string]string{"cluster.failure_domains": "az1"})
	require.EqualError(t, err, `Invalid value for "cluster.failure_domains": ["az1"] are not known failure domains`)

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	require.NoError(t, tx.UpdateNodeFailureDomain(context.Background(), id, "az1"))

	changed, err := config.Patch(context.Background(), tx, map[string]string{"cluster.failure_domains": "az1"})
	require.NoError(t, err)
	assert.Equal(t, "az1", changed["cluster.failure_domains"])
	assert.Equal(t, []string{"az1"}, config.FailureDomains())
}

// TestConfigLoad_FailureDomainsFeatureGate confirms cluster.failure_domains is rejected outright
// while FailureDomainPlacement is disabled (the real-install default, overridden by this
// package's TestMain for every other test in the suite) -- indistinguishable from a build that
// never had this key at all, not merely "value not accepted."
func TestConfigLoad_FailureDomainsFeatureGate(t *testing.T) {
	// IsEnabled reads a cached snapshot populated by LoadFromEnv, not the environment directly --
	// registered before t.Setenv so this cleanup (which runs last, since t.Cleanup unwinds
	// last-registered-first) re-syncs the snapshot only after t.Setenv restores the environment.
	t.Cleanup(func() {
		require.NoError(t, features.LoadFromEnv(features.EnvVar))
	})

	t.Setenv(features.EnvVar, "")
	require.NoError(t, features.LoadFromEnv(features.EnvVar))

	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Patch(context.Background(), tx, map[string]string{"cluster.failure_domains": "az1"})
	require.EqualError(t, err, "Unknown key")
}

// If some previously set values are missing from the ones passed to Replace(),
// they are deleted from the configuration.
func TestConfig_ReplaceDeleteValues(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	config, err := clusterConfig.Load(context.Background(), tx)
	require.NoError(t, err)

	changed, err := config.Replace(context.Background(), tx, map[string]string{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{
		"core.proxy_http": "foo.bar",
		// Validation that the volatile.uuid value cannot change happens in the PUT/PATCH /1.0 API handlers.
		"volatile.uuid": "",
	}, changed)

	_, err = config.Replace(context.Background(), tx, map[string]string{})
	assert.NoError(t, err)

	assert.Empty(t, config.ProxyHTTP())

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

	_, err = config.Replace(context.Background(), tx, map[string]string{"core.proxy_http": "foo.bar"})
	assert.NoError(t, err)

	_, err = config.Patch(context.Background(), tx, map[string]string{})
	assert.NoError(t, err)

	assert.Equal(t, "foo.bar", config.ProxyHTTP())

	values, err := tx.Config(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"core.proxy_http": "foo.bar"}, values)
}
