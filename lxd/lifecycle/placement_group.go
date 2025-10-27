package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// PlacementGroupAction represents a lifecycle event action for placement groups.
type PlacementGroupAction string

// All supported lifecycle events for placement groups.
const (
	PlacementGroupCreated = PlacementGroupAction(api.EventLifecyclePlacementGroupCreated)
	PlacementGroupDeleted = PlacementGroupAction(api.EventLifecyclePlacementGroupDeleted)
	PlacementGroupRenamed = PlacementGroupAction(api.EventLifecyclePlacementGroupRenamed)
	PlacementGroupUpdated = PlacementGroupAction(api.EventLifecyclePlacementGroupUpdated)
)

// Event creates the lifecycle event for an action on a placement group.
func (a PlacementGroupAction) Event(projectName string, placementGroupName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := entity.PlacementGroupURL(projectName, placementGroupName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
