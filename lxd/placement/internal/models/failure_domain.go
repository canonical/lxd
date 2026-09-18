package models

import (
	"context"

	"github.com/canonical/lxd/lxd/db"
)

// FailureDomain identifies a single named failure domain known to the cluster.
type FailureDomain struct {
	ID   uint64
	Name string
}

// MemberFailureDomains maps a cluster member's ID to the failure domain it belongs to. Members
// with no domain assigned ("default") have no entry.
type MemberFailureDomains map[int64]FailureDomain

// LoadMemberFailureDomains builds a MemberFailureDomains for every cluster member with a real
// failure domain assigned.
func LoadMemberFailureDomains(ctx context.Context, tx *db.ClusterTx) (MemberFailureDomains, error) {
	nodes, err := tx.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	addressToDomainID, err := tx.GetNodesFailureDomains(ctx)
	if err != nil {
		return nil, err
	}

	domainNames, err := tx.GetFailureDomainsNames(ctx)
	if err != nil {
		return nil, err
	}

	domains := make(MemberFailureDomains, len(nodes))
	for _, n := range nodes {
		domainID, ok := addressToDomainID[n.Address]
		if !ok || domainID == 0 {
			continue
		}

		domains[n.ID] = FailureDomain{ID: domainID, Name: domainNames[domainID]}
	}

	return domains, nil
}

// Of returns the failure domain assigned to member id, and whether it has one at all.
func (m MemberFailureDomains) Of(id int64) (FailureDomain, bool) {
	d, ok := m[id]
	return d, ok
}
