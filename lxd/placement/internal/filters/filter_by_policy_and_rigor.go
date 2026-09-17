package filters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// errNoEligiblePolicyAndRigor is returned (wrapped) by FilterByPolicyAndRigor when the group's own
// policy and rigor leave no eligible candidates.
var errNoEligiblePolicyAndRigor = errors.New("No eligible candidate cluster member for the group's policy and rigor")

// FilterByPolicyAndRigor applies a placement group's policy/rigor, branching on scope:
// scope=failure-domain buckets candidates by failure domain, keyed by domain name and using
// per-domain instance counts derived from pctx.MemberToInst — a candidate with no assigned failure
// domain can never belong to any domain bucket and is excluded regardless of any allow-list
// already applied. Any other scope buckets candidates directly by cluster member. Either way, this
// only decides which domain(s)/member(s) comply — host selection within compliant domain(s) is a
// separate, later stage.
//
// Only applies when pctx.PlacementGroup is actually set (a real placement group's Name is never
// empty) — an instance with no placement group has no policy/rigor to enforce.
func FilterByPolicyAndRigor(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	apiPlacementGroup := pctx.PlacementGroup
	if apiPlacementGroup.Name == "" {
		return candidates, nil
	}

	scope := apiPlacementGroup.Config["scope"]
	policy := apiPlacementGroup.Config["policy"]
	rigor := apiPlacementGroup.Config["rigor"]

	var filtered []db.NodeInfo
	var err error

	if scope == api.PlacementScopeFailureDomain {
		candidatesByDomain, domains := groupCandidatesByDomain(candidates, pctx.MemberDomains)
		if len(domains) == 0 {
			err = errors.New("No candidate cluster members with an assigned failure domain")
		} else {
			domainToInst := groupInstancesByDomain(pctx.MemberToInst, pctx.MemberDomains)

			var compliantDomains []string
			compliantDomains, err = filterByPolicyAndRigor(policy, rigor, domains, domainToInst)
			for _, d := range compliantDomains {
				filtered = append(filtered, candidatesByDomain[d]...)
			}
		}
	} else {
		memberIDs := make([]int64, len(candidates))
		for i, c := range candidates {
			memberIDs[i] = c.ID
		}

		var compliantIDs []int64
		compliantIDs, err = filterByPolicyAndRigor(policy, rigor, memberIDs, pctx.MemberToInst)

		compliant := sets.New(compliantIDs...)
		isCompliant := func(c db.NodeInfo) bool { return compliant.Contains(c.ID) }
		filtered = slices.Collect(iterutil.Filter(slices.Values(candidates), isCompliant))
	}

	if err != nil {
		return nil, fmt.Errorf("%w: scope %q, policy %q, rigor %q: %w", errNoEligiblePolicyAndRigor, scope, policy, rigor, err)
	}

	return filtered, nil
}

// filterByPolicyAndRigor returns the compliant subset of candidateKeys — cluster member IDs for
// scope=host, or failure domain names for scope=failure-domain — based on the given placement
// policy and rigor, using bucketToInst to look up how many of this group's instances are already
// associated with each key. This is the same algorithm either way; only the key type and what it
// represents changes.
func filterByPolicyAndRigor[K comparable](policy string, rigor string, candidateKeys []K, bucketToInst map[K][]int64) ([]K, error) {
	var compliant []K

	switch {
	case policy == api.PlacementPolicySpread && rigor == api.PlacementRigorStrict:
		// Spread + Strict: place at most one instance per bucket.
		// Filter out candidates that already have instances.
		for _, k := range candidateKeys {
			_, hasInst := bucketToInst[k]
			if !hasInst {
				compliant = append(compliant, k)
			}
		}

		if len(compliant) == 0 {
			return nil, errors.New("No eligible cluster members available")
		}

		return compliant, nil

	case policy == api.PlacementPolicySpread && rigor == api.PlacementRigorPermissive:
		// Spread + Permissive: prefer spreading instances evenly across buckets.
		// The number of instances per bucket differs by at most one.

		// Find the minimum instance count among candidates.
		counts := make([]int, 0, len(candidateKeys))
		for _, k := range candidateKeys {
			counts = append(counts, len(bucketToInst[k]))
		}

		minInstances := 0
		if len(counts) > 0 {
			minInstances = slices.Min(counts)
		}

		// Filter candidates to only those with at most minInstances instances.
		// This ensures the number of instances per bucket differs by at most one.
		for _, k := range candidateKeys {
			instanceCount := len(bucketToInst[k])
			if instanceCount <= minInstances {
				compliant = append(compliant, k)
			}
		}

		if len(compliant) == 0 {
			return nil, errors.New("No eligible cluster members available")
		}

		return compliant, nil

	case policy == api.PlacementPolicyCompact && rigor == api.PlacementRigorStrict:
		// Compact + Strict: place all instances in the same bucket.
		// The bucket with the most instances determines the target.
		if len(bucketToInst) == 0 {
			// No instances yet.
			// All candidates are valid (first instance determines the bucket).
			return candidateKeys, nil
		}

		// Find which bucket has the most instances from this placement group.
		var targetKey K
		maxInstances := -1
		for k, instances := range bucketToInst {
			if len(instances) > maxInstances {
				maxInstances = len(instances)
				targetKey = k
			}
		}

		// Filter candidates to only include the target bucket.
		for _, k := range candidateKeys {
			if k == targetKey {
				compliant = append(compliant, k)
				break
			}
		}

		if len(compliant) == 0 {
			return nil, errors.New("Required cluster member is unavailable")
		}

		return compliant, nil

	case policy == api.PlacementPolicyCompact && rigor == api.PlacementRigorPermissive:
		// Compact + Permissive: prefer to place all instances in the same bucket.
		if len(bucketToInst) == 0 {
			// No instances yet.
			// All candidates are valid (first instance determines the preferred bucket).
			return candidateKeys, nil
		}

		// Find which bucket has the most instances from this placement group.
		var preferredKey K
		maxInstances := -1
		for k, instances := range bucketToInst {
			if len(instances) > maxInstances {
				maxInstances = len(instances)
				preferredKey = k
			}
		}

		// Check if the preferred bucket is in candidates.
		for _, k := range candidateKeys {
			if k == preferredKey {
				// Preferred bucket is available.
				return []K{k}, nil
			}
		}

		// Preferred bucket is not available - fall back to all candidates.
		return candidateKeys, nil

	default:
		return nil, errors.New("Invalid placement group")
	}
}

// groupInstancesByDomain sums each domain's instance IDs from memberToInst, using each member's
// domain assignment. A member with no domain assigned never contributes to any domain's count, so
// switching a group from scope=host to scope=failure-domain doesn't let an unassigned member's
// history skew future domain selection.
func groupInstancesByDomain(memberToInst map[int64][]int64, memberDomains models.MemberFailureDomains) map[string][]int64 {
	domainToInst := make(map[string][]int64, len(memberToInst))
	for memberID, instances := range memberToInst {
		domain, ok := memberDomains.Of(memberID)
		if !ok {
			continue
		}

		domainToInst[domain.Name] = append(domainToInst[domain.Name], instances...)
	}

	return domainToInst
}
