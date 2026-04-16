package lifecycle

import (
	"context"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// InstanceSnapshotAction represents a lifecycle event action for instance snapshots.
type InstanceSnapshotAction string

// All supported lifecycle events for instance snapshots.
const (
	InstanceSnapshotCreated = InstanceSnapshotAction(api.EventLifecycleInstanceSnapshotCreated)
	InstanceSnapshotDeleted = InstanceSnapshotAction(api.EventLifecycleInstanceSnapshotDeleted)
	InstanceSnapshotRenamed = InstanceSnapshotAction(api.EventLifecycleInstanceSnapshotRenamed)
	InstanceSnapshotUpdated = InstanceSnapshotAction(api.EventLifecycleInstanceSnapshotUpdated)
)

// Event creates the lifecycle event for an action on an instance snapshot.
func (a InstanceSnapshotAction) Event(ctx context.Context, inst instance, eventCtx map[string]any) api.EventLifecycle {
	parentName, snapName, _ := api.GetParentAndSnapshotName(inst.Name())

	u := api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(inst.Project().Name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   eventCtx,
		Requestor: request.CreateRequestor(ctx),
	}
}
