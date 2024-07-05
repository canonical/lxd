package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// InstanceSnapshotAction represents a lifecycle event action for instance snapshots.
type InstanceSnapshotAction string

// All supported lifecycle events for instance snapshots.
const (
	InstanceSnapshotCreated = InstanceSnapshotAction("created")
	InstanceSnapshotDeleted = InstanceSnapshotAction("deleted")
	InstanceSnapshotRenamed = InstanceSnapshotAction("renamed")
	InstanceSnapshotUpdated = InstanceSnapshotAction("updated")
)

// Event creates the lifecycle event for an action on an instance snapshot.
func (a InstanceSnapshotAction) Event(inst instance, ctx map[string]interface{}) api.EventLifecycle {
	parentName, instanceName, _ := shared.InstanceGetParentAndSnapshotName(inst.Name())
	u := fmt.Sprintf("/1.0/instances/%s/snapshots/%s", url.PathEscape(parentName), url.PathEscape(instanceName))
	eventType := fmt.Sprintf("instance-snapshot-%s", a)

	if inst.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(inst.Project()))
	}

	var requestor *api.EventLifecycleRequestor
	if inst.Operation() != nil {
		requestor = inst.Operation().Requestor()
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
