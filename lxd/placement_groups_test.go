package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/features"
)

// TestMain turns on the failure-domain-aware placement feature preview for every test in this
// package -- it's off by default for real installs, but placementGroupDefaultConfig/
// placementGroupValidateConfig (exercised directly by tests below) behave as if it doesn't exist
// at all while it's off, which would otherwise break every assertion here that expects scope to
// be defaulted/accepted.
func TestMain(m *testing.M) {
	os.Setenv(features.EnvVar, string(features.FailureDomainPlacement))

	err := features.LoadFromEnv(features.EnvVar)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// TestPlacementGroupDefaultConfig confirms scope is always explicit in the config that ends up
// persisted -- called from both placementGroupsPost and placementGroupPut before validation, so
// every group created or updated from here on stores a real "scope" value instead of relying on
// [cluster.PlacementGroup.ToAPI] to paper over an absent key at read time.
func TestPlacementGroupDefaultConfig(t *testing.T) {
	// Nil config (e.g. a create request that omits "config" entirely): must not panic, and scope
	// still ends up defaulted.
	got := placementGroupDefaultConfig(nil)
	assert.Equal(t, "host", got["scope"])

	// scope missing alongside other keys: defaulted, other keys untouched.
	got = placementGroupDefaultConfig(map[string]string{"policy": "spread", "rigor": "strict"})
	assert.Equal(t, map[string]string{"policy": "spread", "rigor": "strict", "scope": "host"}, got)

	// scope already set to a non-empty value: left alone, whether valid or not -- defaulting is
	// not validation's job, so an invalid value still reaches placementGroupValidateConfig unchanged.
	got = placementGroupDefaultConfig(map[string]string{"scope": "failure-domain"})
	assert.Equal(t, "failure-domain", got["scope"])

	got = placementGroupDefaultConfig(map[string]string{"scope": "bogus"})
	assert.Equal(t, "bogus", got["scope"])
}

// TestPlacementGroupFeatureGate confirms scope is invisible end to end while
// FailureDomainPlacement is disabled (the real-install default, overridden by this package's
// TestMain for every other test in the suite): placementGroupDefaultConfig never synthesizes it,
// and placementGroupValidateConfig rejects it exactly like any other unrecognized key,
// indistinguishable from a build that never had this feature at all.
func TestPlacementGroupFeatureGate(t *testing.T) {
	// IsEnabled reads a cached snapshot populated by LoadFromEnv, not the environment directly --
	// registered before t.Setenv so this cleanup (which runs last, since t.Cleanup unwinds
	// last-registered-first) re-syncs the snapshot only after t.Setenv restores the environment.
	t.Cleanup(func() {
		require.NoError(t, features.LoadFromEnv(features.EnvVar))
	})

	t.Setenv(features.EnvVar, "")
	require.NoError(t, features.LoadFromEnv(features.EnvVar))

	got := placementGroupDefaultConfig(nil)
	assert.NotContains(t, got, "scope")

	err := placementGroupValidateConfig(map[string]string{"policy": "spread", "rigor": "strict", "scope": "host"})
	assert.EqualError(t, err, `Invalid placement group key "scope"`)

	// policy/rigor alone, with no scope at all, are still accepted -- the base feature this gate
	// doesn't touch.
	err = placementGroupValidateConfig(map[string]string{"policy": "spread", "rigor": "strict"})
	assert.NoError(t, err)
}
