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
// gatedAPIExtensions already holds whatever every other file in this package registered via its
// own init() (e.g. cluster_failure_domains, placement_group_calculated_failure_domains) by the
// time this test runs, since it's now the same package-level var those files write to -- unlike
// when this test lived in shared/features, in a build that never linked those init() calls at
// all. This registers one more, real, pre-existing extension name temporarily to exercise the
// mechanism on top of whatever's already there, restoring it afterward so this test doesn't leak
// state into any other test in this package.
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
	assert.Len(t, got, len(version.APIExtensions)-gatedAPIExtensions.Len())

	t.Setenv(features.EnvVar, string(features.FailureDomainPlacement))
	require.NoError(t, features.LoadFromEnv(features.EnvVar))
	assert.True(t, slices.Equal(version.APIExtensions, visibleAPIExtensions()))
}
