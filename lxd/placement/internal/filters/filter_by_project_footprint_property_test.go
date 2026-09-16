package filters

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/shared/api"
)

// TestUnsetFootprintPreservesLegacySpreadPermissiveTieBreak locks in why Filter's unset max_hosts
// case calls getCompliantMembers directly instead of routing through ResolveHostWithinBucket with
// an unbounded (math.MaxInt) default: once every candidate already has at least one instance of
// this group (so none are "unused" by the footprint's binary distinction), the footprint formula
// can't reproduce spread+permissive's finer preference for members with the *fewest* instances --
// it only ever restricts to "used" or "unused", not by count. This is a real, deliberately-chosen
// divergence point, not an oversight; this test exists so nobody "simplifies" Filter back to
// formula-only without re-deriving this.
func TestUnsetFootprintPreservesLegacySpreadPermissiveTieBreak(t *testing.T) {
	candidates := []db.NodeInfo{
		{ID: 1, Name: "host1"},
		{ID: 2, Name: "host2"},
		{ID: 3, Name: "host3"},
		{ID: 4, Name: "host4"},
		{ID: 5, Name: "host5"},
	}

	// Every candidate already has at least one instance of this group -- uneven counts, but
	// none are "unused" by the used/unused distinction ResolveHostWithinBucket relies on.
	memberToInst := map[int64][]int64{
		1: {101, 102, 103},
		2: {104, 105},
		3: {106, 107},
		4: {108},
		5: {109},
	}

	legacy, err := getCompliantMembers(api.PlacementPolicySpread, api.PlacementRigorPermissive, candidates, memberToInst)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []db.NodeInfo{candidates[3], candidates[4]}, legacy, "legacy per-member-count logic should narrow to the two members tied at the minimum count (1 each)")

	usedHosts := make(map[int64]struct{}, len(candidates))
	for _, c := range candidates {
		usedHosts[c.ID] = struct{}{}
	}

	formulaOnly, err := ResolveHostWithinBucket(api.PlacementRigorPermissive, candidates, usedHosts, math.MaxInt)
	assert.NoError(t, err)
	assert.ElementsMatch(t, candidates, formulaOnly, "the footprint formula alone can't reproduce the per-member-count narrowing -- it falls back to every candidate once none are unused, confirming the two paths genuinely diverge here")
}

// genHostCandidates draws a non-empty slice of distinct db.NodeInfo, IDs 1..n.
func genHostCandidates(t *rapid.T) []db.NodeInfo {
	n := rapid.IntRange(1, 6).Draw(t, "num-hosts")
	candidates := make([]db.NodeInfo, n)
	for i := range candidates {
		candidates[i] = db.NodeInfo{ID: int64(i + 1), Name: fmt.Sprintf("host%d", i+1)}
	}

	return candidates
}

// genUsedHosts draws a random subset of candidates' IDs as the current footprint.
func genUsedHosts(t *rapid.T, candidates []db.NodeInfo) map[int64]struct{} {
	used := make(map[int64]struct{}, len(candidates))
	for _, c := range candidates {
		if rapid.Bool().Draw(t, fmt.Sprintf("used-%d", c.ID)) {
			used[c.ID] = struct{}{}
		}
	}

	return used
}

// genSizeBound draws either an unbounded (math.MaxInt) or a finite bound in [0, max].
func genSizeBound(t *rapid.T, label string, max int) int {
	if rapid.Bool().Draw(t, label+"-unbounded") {
		return math.MaxInt
	}

	return rapid.IntRange(0, max).Draw(t, label)
}

func nodeIDs(nodes []db.NodeInfo) []int64 {
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}

	return ids
}

// TestResolveHostWithinBucketSubsetProperty asserts the universal sanity check: whatever is
// returned is always a subset of the candidates given.
func TestResolveHostWithinBucketSubsetProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		candidates := genHostCandidates(t)
		usedHosts := genUsedHosts(t, candidates)
		maxHosts := genSizeBound(t, "max", len(candidates)+2)
		rigor := genRigor(t)

		got, err := ResolveHostWithinBucket(rigor, candidates, usedHosts, maxHosts)
		if err != nil {
			return
		}

		for _, c := range got {
			assert.Contains(t, nodeIDs(candidates), c.ID)
		}
	})
}

// TestResolveHostWithinBucketGrowProperty asserts the below-the-cap invariant: whenever the
// footprint is below maxHosts and at least one live candidate isn't already part of it, the
// result never includes an already-used host — every returned host is a genuine footprint
// expansion. This is now the unconditional default direction (no policy branch), so it's asserted
// without ever passing a policy in.
func TestResolveHostWithinBucketGrowProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		candidates := genHostCandidates(t)
		usedHosts := genUsedHosts(t, candidates)

		// Force the grow regime: maxHosts strictly greater than the current footprint.
		maxHosts := len(usedHosts) + 1 + rapid.IntRange(0, 2).Draw(t, "max-slack")
		rigor := genRigor(t)

		hasUnused := len(usedHosts) < len(candidates)

		got, err := ResolveHostWithinBucket(rigor, candidates, usedHosts, maxHosts)

		if !hasUnused {
			if rigor == api.PlacementRigorStrict {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, candidates, got)
			}

			return
		}

		assert.NoError(t, err)
		for _, c := range got {
			_, alreadyUsed := usedHosts[c.ID]
			assert.False(t, alreadyUsed, "grow regime returned an already-used host %d", c.ID)
		}

		assert.NotEmpty(t, got)
	})
}

// TestResolveHostWithinBucketCeilingProperty asserts the at/above-the-cap invariant: whenever the
// footprint has reached maxHosts and at least one footprint host is live, every returned host is
// already part of the existing footprint — never a fresh one.
func TestResolveHostWithinBucketCeilingProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		candidates := genHostCandidates(t)
		usedHosts := genUsedHosts(t, candidates)

		// Force the ceiling regime: maxHosts at or below the current footprint.
		maxHosts := rapid.IntRange(0, len(usedHosts)).Draw(t, "max-forced")
		rigor := genRigor(t)

		hasUsedLive := len(usedHosts) > 0

		got, err := ResolveHostWithinBucket(rigor, candidates, usedHosts, maxHosts)

		if !hasUsedLive {
			if rigor == api.PlacementRigorStrict {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, candidates, got)
			}

			return
		}

		assert.NoError(t, err)
		for _, c := range got {
			_, alreadyUsed := usedHosts[c.ID]
			assert.True(t, alreadyUsed, "ceiling regime returned a fresh host %d", c.ID)
		}

		assert.NotEmpty(t, got)
	})
}
