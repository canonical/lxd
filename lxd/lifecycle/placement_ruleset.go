package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// PlacementRulesetAction represents a lifecycle event action for a placement ruleset.
type PlacementRulesetAction string

// All supported lifecycle events for placement rulesets.
const (
	PlacementRulesetCreated = PlacementRulesetAction(api.EventLifecyclePlacementRulesetCreated)
	PlacementRulesetUpdated = PlacementRulesetAction(api.EventLifecyclePlacementRulesetUpdated)
	PlacementRulesetRenamed = PlacementRulesetAction(api.EventLifecyclePlacementRulesetRenamed)
	PlacementRulesetDeleted = PlacementRulesetAction(api.EventLifecyclePlacementRulesetDeleted)
)

// Event creates the lifecycle event for an action on a Certificate.
func (a PlacementRulesetAction) Event(projectName string, rulesetName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := entity.PlacementRulesetURL(projectName, rulesetName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
