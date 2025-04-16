package placement

import (
	"context"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/entity"
)

// GetNonConformantInstances returns a map of instance ID to cluster member of all instances that have the given ruleset and are not conformant to it.
func GetNonConformantInstances(ctx context.Context, tx *db.ClusterTx, rulesetName string, projectName string, candidates []db.NodeInfo, allMembers []db.NodeInfo) (map[int]db.NodeInfo, error) {
	selector := cluster.Selector{
		EntityType: cluster.EntityType(entity.TypeInstance),
		Matchers: cluster.SelectorMatchers{
			{
				Property: "config.placement.ruleset",
				Values:   []string{rulesetName},
			},
			{
				Property: "project",
				Values:   []string{projectName},
			},
		},
	}

	instanceIDs, err := cluster.RunSelector(ctx, tx.Tx(), selector)
	if err != nil {
		return nil, err
	}

	args := make([]any, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		args = append(args, id)
	}

	instanceToMember := make(map[int]int)
	err = query.Scan(ctx, tx.Tx(), `SELECT id, node_id FROM instances WHERE id IN `+query.Params(len(instanceIDs)), func(scan func(dest ...any) error) error {
		var instanceID int
		var memberID int
		err := scan(&instanceID, &memberID)
		if err != nil {
			return err
		}

		instanceToMember[instanceID] = memberID
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	nonConformantInstances := make(map[int]db.NodeInfo, len(instanceToMember))

outer:
	for instanceID, memberID := range instanceToMember {
		for _, candidate := range candidates {
			if memberID == int(candidate.ID) {
				continue outer
			}
		}

		var location db.NodeInfo
		for _, member := range allMembers {
			if memberID == int(member.ID) {
				location = member
			}
		}

		nonConformantInstances[instanceID] = location
	}

	return nonConformantInstances, nil
}
