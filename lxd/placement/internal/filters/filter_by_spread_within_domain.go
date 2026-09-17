package filters

import (
	"context"
	"maps"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// FilterBySpreadWithinDomain applies the always-spread-within-domain host-level rule
// independently to each domain represented among candidates (already narrowed to compliant
// domains by FilterByPolicyAndRigor), unioning the results: within each domain, prefer a host this
// group hasn't already used there, falling back to every host in the domain once none are unused.
//
// This never varies by policy or rigor: policy/rigor already spent their effect at the
// domain-selection layer (FilterByPolicyAndRigor) — once a domain is compliant, placement within
// it always spreads, unconditionally. There is no placement-group-level footprint cap to enforce
// here (removed from the design well before this package existed) — this only ever prefers a host
// this group hasn't already used in the domain, so it doesn't route through ResolveHostWithinBucket
// (that function's whole point is comparing a footprint against a bound; passing it a bound that
// can never be reached would just be dead code pretending to check something). limits.max_hosts,
// when set, is a separate, project-wide, cross-domain total enforced by FilterByProjectFootprint
// on the engine's final combined candidate list, later in the chain — it is never compared against
// any single domain's own host count.
//
// Only applies to scope=failure-domain; a no-op passthrough for scope=host (host-level compliance
// was already fully decided by FilterByPolicyAndRigor) or when there's no placement group at all.
func FilterBySpreadWithinDomain(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	apiPlacementGroup := pctx.PlacementGroup
	if apiPlacementGroup.Config["scope"] != api.PlacementScopeFailureDomain {
		return candidates, nil
	}

	candidatesByDomain, domains := groupCandidatesByDomain(candidates, pctx.MemberDomains)

	var filtered []db.NodeInfo
	for _, domain := range domains {
		usedHosts := hostsUsedInDomain(pctx.MemberToInst, pctx.MemberDomains, domain)
		filtered = append(filtered, spreadWithinDomain(candidatesByDomain[domain], usedHosts)...)
	}

	return filtered, nil
}

// spreadWithinDomain narrows candidates within one compliant domain to those not in usedHosts —
// spreading this group's footprint within the domain onto a host it hasn't already used —
// falling back to every candidate in the domain once none are unused. This never varies by rigor:
// rigor governs whether a group's policy at the domain-selection layer (FilterByPolicyAndRigor)
// can be satisfied, not how a host is picked once a domain already qualifies, so this can never
// fail — every domain FilterByPolicyAndRigor already deemed compliant contributes at least one
// host here.
func spreadWithinDomain(candidates []db.NodeInfo, usedHosts sets.Set[int64]) []db.NodeInfo {
	isUnused := func(c db.NodeInfo) bool { return !usedHosts.Contains(c.ID) }

	unused := slices.Collect(iterutil.Filter(slices.Values(candidates), isUnused))
	if len(unused) > 0 {
		return unused
	}

	return candidates
}

// hostsUsedInDomain returns the set of member IDs, among memberToInst's keys, whose assigned
// domain is domain — i.e. this placement group's current footprint within that one domain only.
// This is never the right set to compare against a project-wide limit: see
// FilterBySpreadWithinDomain.
func hostsUsedInDomain(memberToInst map[int64][]int64, memberDomains models.MemberFailureDomains, domain string) sets.Set[int64] {
	inDomain := func(memberID int64) bool {
		d, ok := memberDomains.Of(memberID)
		return ok && d.Name == domain
	}

	return sets.FromSeq(iterutil.Filter(maps.Keys(memberToInst), inDomain))
}
