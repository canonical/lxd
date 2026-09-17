package filters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"

	"github.com/canonical/lxd/shared/api"
)

// filterByPolicyAndRigor is exercised here directly with string keys standing in for failure
// domain names — the same generic function is used with int64 member IDs for scope=host, but the
// algorithm itself doesn't care about the key type, so testing it once with strings covers both.

// genNonEmptyDomainSubset draws a non-empty random subset of pool.
func genNonEmptyDomainSubset(t *rapid.T, pool []string, label string) []string {
	subset := genDomainSubset(t, pool, label)
	if len(subset) == 0 {
		return []string{rapid.SampledFrom(pool).Draw(t, label+"-fallback")}
	}

	return subset
}

// genBucketInstanceCounts assigns each candidate key an independently-drawn number of instances
// (0-3), used as bucketToInst's per-key instance list length. Keys with 0 are omitted, matching
// how real callers never store an empty instance list for a bucket.
func genBucketInstanceCounts(t *rapid.T, keys []string) map[string][]int64 {
	counts := make(map[string][]int64, len(keys))
	var nextID int64
	for _, k := range keys {
		n := rapid.IntRange(0, 3).Draw(t, "count-"+k)
		if n == 0 {
			continue
		}

		instances := make([]int64, n)
		for i := range instances {
			instances[i] = nextID
			nextID++
		}

		counts[k] = instances
	}

	return counts
}

// TestFilterByPolicyAndRigorSubsetProperty asserts the universal sanity check: whatever
// filterByPolicyAndRigor returns is always a subset of the candidate keys it was given.
func TestFilterByPolicyAndRigorSubsetProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keys := genNonEmptyDomainSubset(t, domainPool, "keys")
		bucketToInst := genBucketInstanceCounts(t, keys)
		policy := genPolicy(t)
		rigor := genRigor(t)

		compliant, err := filterByPolicyAndRigor(policy, rigor, keys, bucketToInst)
		if err != nil {
			return
		}

		for _, k := range compliant {
			assert.Contains(t, keys, k)
		}
	})
}

// TestFilterByPolicyAndRigorSpreadStrictExcludesUsedProperty asserts spread+strict's defining
// invariant: a bucket that already has any instance is never compliant.
func TestFilterByPolicyAndRigorSpreadStrictExcludesUsedProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keys := genNonEmptyDomainSubset(t, domainPool, "keys")
		bucketToInst := genBucketInstanceCounts(t, keys)

		compliant, err := filterByPolicyAndRigor(api.PlacementPolicySpread, api.PlacementRigorStrict, keys, bucketToInst)
		if err != nil {
			// Only possible when every key already has an instance.
			for _, k := range keys {
				assert.NotEmpty(t, bucketToInst[k])
			}

			return
		}

		for _, k := range compliant {
			assert.Empty(t, bucketToInst[k])
		}
	})
}

// TestFilterByPolicyAndRigorCompactValidTieBreakSetProperty asserts compact's contract without
// forcing determinism: when instances already exist, the result is always a subset of the keys
// tied for the maximum instance count among the candidates — which one wins an exact tie is
// intentionally left unspecified (map iteration order), matching this codebase's existing
// tie-break philosophy elsewhere.
func TestFilterByPolicyAndRigorCompactValidTieBreakSetProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keys := genNonEmptyDomainSubset(t, domainPool, "keys")
		bucketToInst := genBucketInstanceCounts(t, keys)
		rigor := genRigor(t)

		compliant, err := filterByPolicyAndRigor(api.PlacementPolicyCompact, rigor, keys, bucketToInst)

		if len(bucketToInst) == 0 {
			assert.NoError(t, err)
			assert.ElementsMatch(t, keys, compliant)
			return
		}

		// bucketToInst's keys are always drawn from keys, so the preferred/target key is always
		// itself a candidate — an error here would be a real regression, not an expected outcome.
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		maxCount := -1
		for _, k := range keys {
			if len(bucketToInst[k]) > maxCount {
				maxCount = len(bucketToInst[k])
			}
		}

		for _, k := range compliant {
			assert.Equal(t, maxCount, len(bucketToInst[k]))
		}
	})
}

// TestFilterByPolicyAndRigorPermissiveNeverFailsProperty asserts the rigor fallback contract:
// permissive regimes never return an error when given at least one candidate key.
func TestFilterByPolicyAndRigorPermissiveNeverFailsProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keys := genNonEmptyDomainSubset(t, domainPool, "keys")
		bucketToInst := genBucketInstanceCounts(t, keys)
		policy := genPolicy(t)

		_, err := filterByPolicyAndRigor(policy, api.PlacementRigorPermissive, keys, bucketToInst)
		assert.NoError(t, err)
	})
}
