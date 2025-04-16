package placement

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ApplyRuleset checks if the given configuration contains a placement ruleset. If not, it returns the candidates
// unaltered. If so, it filters the candidates via the found ruleset.
func ApplyRuleset(ctx context.Context, tx *sql.Tx, candidates []db.NodeInfo, expandedConfig map[string]string, runningInstanceID *int, ruleset cluster.PlacementRuleset) ([]db.NodeInfo, error) {
	rules := ruleset.SortedRules()

	entityIDsToMemberIDs := func(entityType entity.Type, entityIDs []int) ([]int, error) {
		args := make([]any, 0, len(entityIDs))
		for _, id := range entityIDs {
			args = append(args, id)
		}

		switch entityType {
		case entity.TypeInstance:
			return query.SelectIntegers(ctx, tx, `SELECT DISTINCT node_id FROM instances WHERE id IN `+query.Params(len(entityIDs)), args...)
		case entity.TypeClusterGroup:
			return query.SelectIntegers(ctx, tx, `SELECT DISTINCT node_id FROM nodes_cluster_groups WHERE group_id IN `+query.Params(len(entityIDs)), args...)
		case entity.TypeClusterMember:
			return entityIDs, nil
		default:
			return nil, fmt.Errorf("Invalid placement rule selector entity type %q", entityType)
		}
	}

	isSelfMatch := func(selector cluster.Selector) bool {
		if entity.Type(selector.EntityType) != entity.TypeInstance {
			return false
		}

		for _, m := range selector.Matchers {
			_, key, ok := strings.Cut(m.Property, "config.")
			if !ok {
				continue
			}

			if shared.ValueInSlice(expandedConfig[key], m.Values) {
				return true
			}

			return false
		}

		return false
	}

	for _, rule := range rules {
		entityIDs, err := cluster.RunSelector(ctx, tx, rule.Selector)
		if err != nil {
			return nil, err
		}

		// Omit current instance from rule application.
		entityType := entity.Type(rule.Selector.EntityType)
		if runningInstanceID != nil && entityType == entity.TypeInstance {
			entityIDs = slices.DeleteFunc(entityIDs, func(i int) bool {
				return entityIDs[i] == *runningInstanceID
			})
		}

		if len(entityIDs) == 0 {
			if rule.Required {
				if isSelfMatch(rule.Selector) {
					continue
				}

				return nil, api.StatusErrorf(http.StatusBadRequest, "Required affinity rule selector did not match any entities of type %q", entityType)
			}

			return candidates, nil
		}

		memberIDs, err := entityIDsToMemberIDs(entityType, entityIDs)
		if err != nil {
			return nil, err
		}

		kind := api.PlacementRuleKind(rule.Kind)
		newCandidates := make([]db.NodeInfo, 0, len(candidates))
		for _, candidate := range candidates {
			candidatePresentInSelection := shared.ValueInSlice(int(candidate.ID), memberIDs)
			switch {
			case candidatePresentInSelection && kind == api.PlacementRuleKindMemberAffinity:
				newCandidates = append(newCandidates, candidate)
			case !candidatePresentInSelection && kind == api.PlacementRuleKindMemberAntiAffinity:
				newCandidates = append(newCandidates, candidate)
			}
		}

		if len(newCandidates) == 0 {
			if rule.Required {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Required affinity rule selector removed all candidates from placement selection")
			}

			return candidates, nil
		}

		candidates = newCandidates
	}

	return candidates, nil
}
