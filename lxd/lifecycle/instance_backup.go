package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// InstanceBackupAction represents a lifecycle event action for instance backups.
type InstanceBackupAction string

// All supported lifecycle events for instance backups.
const (
	InstanceBackupCreated   = InstanceBackupAction(api.EventLifecycleInstanceBackupCreated)
	InstanceBackupDeleted   = InstanceBackupAction(api.EventLifecycleInstanceBackupDeleted)
	InstanceBackupRenamed   = InstanceBackupAction(api.EventLifecycleInstanceBackupRenamed)
	InstanceBackupRetrieved = InstanceBackupAction(api.EventLifecycleInstanceBackupRetrieved)
)

// Event creates the lifecycle event for an action on an instance backup.
func (a InstanceBackupAction) Event(name string, inst instance, ctx map[string]any) api.EventLifecycle {
	parentName, instanceName, _ := shared.InstanceGetParentAndSnapshotName(name)

	u := fmt.Sprintf("/1.0/instances/%s/backups/%s", url.PathEscape(parentName), url.PathEscape(instanceName))
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
