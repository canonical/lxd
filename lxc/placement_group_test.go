package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/api"
)

// TestPlacementGroupListColumnData covers the scope/calculated-failure-domain columns, including
// a group with none of the new keys set (rendering empty, not erroring) and two different
// scope=failure-domain groups showing the identical calculated failure-domain value (there's no
// per-group override anymore, so this is expected, not a bug).
func TestPlacementGroupListColumnData(t *testing.T) {
	c := &cmdPlacementGroupList{}

	plain := api.PlacementGroup{
		Config: map[string]string{"policy": "spread", "rigor": "strict"},
	}
	assert.Empty(t, c.scopeColumnData(plain))
	assert.Empty(t, c.calculatedFailureDomainsColumnData(plain))

	groupA := api.PlacementGroup{
		Name: "group-a",
		Config: map[string]string{
			"policy": "spread",
			"rigor":  "permissive",
			"scope":  api.PlacementScopeFailureDomain,
		},
		CalculatedFailureDomains: []string{"fd1", "fd2"},
	}
	groupB := api.PlacementGroup{
		Name: "group-b",
		Config: map[string]string{
			"policy": "compact",
			"rigor":  "strict",
			"scope":  api.PlacementScopeFailureDomain,
		},
		CalculatedFailureDomains: []string{"fd1", "fd2"},
	}
	assert.Equal(t, api.PlacementScopeFailureDomain, c.scopeColumnData(groupA))
	assert.Equal(t, "fd1,fd2", c.calculatedFailureDomainsColumnData(groupA))
	assert.Equal(t, c.calculatedFailureDomainsColumnData(groupA), c.calculatedFailureDomainsColumnData(groupB), "two groups with no per-group override should show the identical calculated set")
}
