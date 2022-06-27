package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
func (a InstanceSnapshotAction) Event(inst instance, ctx map[string]any) api.EventLifecycle {
	parentName, instanceName, _ := shared.InstanceGetParentAndSnapshotName(inst.Name())

	u := fmt.Sprintf("/1.0/instances/%s/snapshots/%s", url.PathEscape(parentName), url.PathEscape(instanceName))
	if inst.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(inst.Project()))
	}

	var requestor *api.EventLifecycleRequestor
	if inst.Operation() != nil {
		requestor = inst.Operation().Requestor()
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
