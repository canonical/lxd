package filters

import (
	"pgregory.net/rapid"

	"github.com/canonical/lxd/shared/api"
)

// domainPool is deliberately small so generated subsets overlap often — the point of the property
// tests that use it is to exercise real narrowing/intersection behavior, not near-certainly-disjoint
// random sets.
var domainPool = []string{"fd1", "fd2", "fd3", "fd4", "fd5"}

// genDomainSubset draws a random subset of pool, preserving pool's order (and therefore
// distinctness), by independently flipping a coin for each element.
func genDomainSubset(t *rapid.T, pool []string, label string) []string {
	var subset []string
	for _, name := range pool {
		if rapid.Bool().Draw(t, label+"-"+name) {
			subset = append(subset, name)
		}
	}

	return subset
}

// genPolicy and genRigor live here, alongside the other generic rapid generators this package's
// property tests share, rather than in whichever property-test file happens to need them first.
func genPolicy(t *rapid.T) string {
	return rapid.SampledFrom([]string{api.PlacementPolicySpread, api.PlacementPolicyCompact}).Draw(t, "policy")
}

func genRigor(t *rapid.T) string {
	return rapid.SampledFrom([]string{api.PlacementRigorStrict, api.PlacementRigorPermissive}).Draw(t, "rigor")
}
