package placement

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ValidateRuleset performs ruleset validation by initially converting the ruleset into a cluster.PlacementRuleset, then
// performs a dry-run of placement logic. If the placement logic returns an error, the rule is not valid.
func ValidateRuleset(ctx context.Context, tx *db.ClusterTx, projectName string, newRuleset api.PlacementRuleset) (validatedRuleset *cluster.PlacementRuleset, filteredCandidates []db.NodeInfo, allMembers []db.NodeInfo, err error) {
	// Get the project
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return nil, nil, nil, err
	}

	// Expand project config
	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, nil, nil, err
	}

	// Convert from API type, validating ruleset and project limits.
	clusterGroupsAllowed := limits.GetRestrictedClusterGroups(project)
	dbRuleset, err := cluster.PlacementRulesetFromAPI(newRuleset, shared.IsTrue(project.Config["restricted"]), clusterGroupsAllowed)
	if err != nil {
		return nil, nil, nil, err
	}

	// Pass instance configuration into the scheduler. That will allow instances to self-match.
	// For example, if creating a rule with required cluster member affinity to another instance with a particular
	// config key, we want to allow the first instance of this kind to be placed. So the rule is not invalid.
	instanceConfig := make(map[string]string)
	for _, rule := range dbRuleset.PlacementRules {
		if entity.Type(rule.Selector.EntityType) == entity.TypeInstance {
			for _, matcher := range rule.Selector.Matchers {
				key, ok := strings.CutPrefix(matcher.Property, "config.")
				if !ok {
					continue
				}

				// Allow self match
				instanceConfig[key] = matcher.Values[0]
			}
		}
	}

	// Get current nodes.
	allMembers, err = tx.GetNodes(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed getting cluster members: %w", err)
	}

	// Get initial candidates.
	candidateMembers, err := tx.GetCandidateMembers(ctx, allMembers, nil, "", clusterGroupsAllowed, db.DefaultOfflineThreshold*time.Second)
	if err != nil {
		return nil, nil, nil, err
	}

	// Dry-run the placement rules. This will error if there are no candidates after running the ruleset.
	candidates, err := ApplyRuleset(ctx, tx.Tx(), candidateMembers, instanceConfig, nil, *dbRuleset)
	if err != nil {
		return nil, nil, allMembers, err
	}

	return dbRuleset, candidates, allMembers, nil
}
