package cluster

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/features"
)

// TestMain turns on the failure-domain-aware placement feature preview for every test in this
// package -- it's off by default for real installs, but ToAPI's scope backward-compatibility
// normalization (exercised below) is itself gated, and skips normalizing scope at all while the
// preview is off.
func TestMain(m *testing.M) {
	os.Setenv(features.EnvVar, string(features.FailureDomainPlacement))

	err := features.LoadFromEnv(features.EnvVar)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// TestPlacementGroupToAPIScopeBackwardCompatibility covers scope, which was added after
// policy/rigor and was never made required: a placement group created (or last updated) before
// scope existed has no "scope" entry in its stored config at all. ToAPI must normalize that to
// the explicit "host" value -- what an absent scope has always meant -- rather than surfacing an
// empty string, so API responses and callers like lxc placement-group list's SCOPE column never
// have to special-case "missing" versus "host" themselves.
func TestPlacementGroupToAPIScopeBackwardCompatibility(t *testing.T) {
	group := PlacementGroup{
		Row:         PlacementGroupsRow{ID: 1, Name: "pg1"},
		ProjectName: "default",
	}

	cases := []struct {
		name      string
		configs   map[int64]map[string]string
		wantScope string
	}{
		{
			name:      "no config entry at all for this group",
			configs:   map[int64]map[string]string{},
			wantScope: api.PlacementScopeHost,
		},
		{
			name: "config present but scope key absent (pre-scope group)",
			configs: map[int64]map[string]string{
				1: {"policy": api.PlacementPolicySpread, "rigor": api.PlacementRigorStrict},
			},
			wantScope: api.PlacementScopeHost,
		},
		{
			name: "scope explicitly set to host",
			configs: map[int64]map[string]string{
				1: {"policy": api.PlacementPolicySpread, "rigor": api.PlacementRigorStrict, "scope": api.PlacementScopeHost},
			},
			wantScope: api.PlacementScopeHost,
		},
		{
			name: "scope explicitly set to failure-domain is preserved unchanged",
			configs: map[int64]map[string]string{
				1: {"policy": api.PlacementPolicySpread, "rigor": api.PlacementRigorStrict, "scope": api.PlacementScopeFailureDomain},
			},
			wantScope: api.PlacementScopeFailureDomain,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := group.ToAPI(tt.configs)
			assert.Equal(t, tt.wantScope, got.Config["scope"])
		})
	}
}

// TestPlacementGroupToAPIDoesNotMutateInputConfigs guards against a real regression: ToAPI must
// never write the normalized "scope" back into the configs map it was given. placementGroupPut
// reads that same configs map again, after calling ToAPI, to build the PATCH merge base -- if
// ToAPI mutated it in place, a PATCH request that never touches "scope" would silently persist a
// synthesized "scope": "host" that was never actually stored.
func TestPlacementGroupToAPIDoesNotMutateInputConfigs(t *testing.T) {
	group := PlacementGroup{
		Row:         PlacementGroupsRow{ID: 1, Name: "pg1"},
		ProjectName: "default",
	}

	configs := map[int64]map[string]string{
		1: {"policy": api.PlacementPolicySpread, "rigor": api.PlacementRigorStrict},
	}

	got := group.ToAPI(configs)

	assert.Equal(t, api.PlacementScopeHost, got.Config["scope"])
	_, ok := configs[1]["scope"]
	assert.False(t, ok, "ToAPI must not write scope back into the caller's configs map")
}

// TestPlacementGroupToAPIFeatureGate confirms scope is invisible in ToAPI's output while
// FailureDomainPlacement is disabled (the real-install default, overridden by this package's
// TestMain for every other test in the suite) -- no key at all, not even normalized to "host",
// matching a build that never had this feature.
func TestPlacementGroupToAPIFeatureGate(t *testing.T) {
	// IsEnabled reads a cached snapshot populated by LoadFromEnv, not the environment directly --
	// registered before t.Setenv so this cleanup (which runs last, since t.Cleanup unwinds
	// last-registered-first) re-syncs the snapshot only after t.Setenv restores the environment.
	t.Cleanup(func() {
		require.NoError(t, features.LoadFromEnv(features.EnvVar))
	})

	t.Setenv(features.EnvVar, "")
	require.NoError(t, features.LoadFromEnv(features.EnvVar))

	group := PlacementGroup{
		Row:         PlacementGroupsRow{ID: 1, Name: "pg1"},
		ProjectName: "default",
	}

	got := group.ToAPI(map[int64]map[string]string{
		1: {"policy": api.PlacementPolicySpread, "rigor": api.PlacementRigorStrict},
	})

	_, ok := got.Config["scope"]
	assert.False(t, ok, "scope must not appear at all while the feature preview is off")
}
