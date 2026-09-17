package filters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/failuredomain"
	. "github.com/canonical/lxd/lxd/internal/func/predicate"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// errNoEligibleFailureDomain is returned (wrapped) by FilterByClusterFailureDomains when the
// cluster-wide allow-list leaves no eligible candidates.
var errNoEligibleFailureDomain = errors.New("No candidate cluster members in an eligible failure domain")

// FilterByClusterFailureDomains applies the cluster-wide operational allow-list
// (pctx.ClusterFailureDomains): only applies when the override is actually set and a placement
// group is in play (pctx.MemberDomains, populated by LoadPlacementGroupMembers, is otherwise never
// filled in), so a cluster with no override — or an instance with no placement group at all —
// stays fully domain-agnostic regardless of scope. A member with no assigned failure domain can
// never match a named domain, so it drops out once the allow-list is in effect. This applies
// regardless of scope — it's the cluster-wide incident-response lever, not something tied to any
// one group's own scheduling mode.
func FilterByClusterFailureDomains(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	if pctx.PlacementGroup.Name == "" || len(pctx.ClusterFailureDomains) == 0 {
		// This is the backward compatible path and the most common path with failure-domain filtering because typically
		// the `cluster.failure_domains` override is not set.
		return candidates, nil
	}

	names, err := tx.GetKnownFailureDomainNames(ctx)
	if err != nil {
		return nil, err
	}

	calculated := failuredomain.ResolveCalculatedFailureDomains(names, pctx.ClusterFailureDomains)

	hasDomain := func(c db.NodeInfo) bool {
		_, ok := pctx.MemberDomains.Of(c.ID)
		return ok
	}

	domainIsCalculated := func(c db.NodeInfo) bool {
		domain, _ := pctx.MemberDomains.Of(c.ID)
		return calculated.Contains(domain.Name)
	}

	hasValidDomain := And(hasDomain, domainIsCalculated)

	allowed := slices.Collect(iterutil.Filter(slices.Values(candidates), hasValidDomain))
	if len(allowed) == 0 {
		validDomains := calculated.Slice()
		slices.Sort(validDomains)

		return nil, fmt.Errorf("%w (valid domains: %v)", errNoEligibleFailureDomain, validDomains)
	}

	return allowed, nil
}
