package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ReplicatorAction represents a lifecycle event action for replicators.
type ReplicatorAction string

// All supported lifecycle events for replicators.
const (
	ReplicatorCreated = ReplicatorAction(api.EventLifecycleReplicatorCreated)
	ReplicatorDeleted = ReplicatorAction(api.EventLifecycleReplicatorDeleted)
	ReplicatorRenamed = ReplicatorAction(api.EventLifecycleReplicatorRenamed)
	ReplicatorRun     = ReplicatorAction(api.EventLifecycleReplicatorRun)
	ReplicatorUpdated = ReplicatorAction(api.EventLifecycleReplicatorUpdated)
)

// Event creates the lifecycle event for an action on a replicator.
func (a ReplicatorAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "replicators", name).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
