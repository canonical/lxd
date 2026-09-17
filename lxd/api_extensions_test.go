package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/features"
	"github.com/canonical/lxd/shared/version"
)

// TestVisibleAPIExtensions confirms gatedAPIExtensions are stripped from what's advertised to
// clients while features.FailureDomainPlacement is disabled, and that every other extension --
// and the full list once the feature is enabled -- passes through unchanged.
//
// gatedAPIExtensions is empty at this point in history -- nothing has registered itself yet -- so
// this registers a real, pre-existing extension name temporarily to exercise the mechanism,
// restoring it afterward so this test doesn't leak state into any other test in this package.
func TestVisibleAPIExtensions(t *testing.T) {
	// features.IsEnabled reads a cached snapshot populated by features.LoadFromEnv, not the
	// environment directly -- unlike t.Setenv, which only restores the variable itself.
	// Registered before either t.Setenv call below so this cleanup (which runs last, since
	// t.Cleanup unwinds last-registered-first) re-syncs the snapshot only after t.Setenv has
	// restored the real environment.
	t.Cleanup(func() {
		require.NoError(t, features.LoadFromEnv(features.EnvVar))
	})

	t.Setenv(features.EnvVar, "")
	require.NoError(t, features.LoadFromEnv(features.EnvVar))

	const testExtension = "storage_zfs_remove_snapshots"
	gatedAPIExtensions.Add(testExtension)
	defer gatedAPIExtensions.Remove(testExtension)

	got := visibleAPIExtensions()
	assert.NotContains(t, got, testExtension)
	assert.Len(t, got, len(version.APIExtensions)-1)

	t.Setenv(features.EnvVar, string(features.FailureDomainPlacement))
	require.NoError(t, features.LoadFromEnv(features.EnvVar))
	assert.True(t, slices.Equal(version.APIExtensions, visibleAPIExtensions()))
}
