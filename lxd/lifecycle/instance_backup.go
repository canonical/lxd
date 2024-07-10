package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// InstanceBackupAction represents a lifecycle event action for instance backups.
type InstanceBackupAction string

// All supported lifecycle events for instance backups.
const (
	InstanceBackupCreated   = InstanceBackupAction("created")
	InstanceBackupDeleted   = InstanceBackupAction("deleted")
	InstanceBackupRenamed   = InstanceBackupAction("renamed")
	InstanceBackupRetrieved = InstanceBackupAction("retrieved")
)

// Event creates the lifecycle event for an action on an instance backup.
func (a InstanceBackupAction) Event(name string, inst instance, ctx map[string]interface{}) api.EventLifecycle {
	parentName, instanceName, _ := shared.InstanceGetParentAndSnapshotName(name)
	u := fmt.Sprintf("/1.0/instances/%s/backups/%s", url.PathEscape(parentName), url.PathEscape(instanceName))
	eventType := fmt.Sprintf("instance-backup-%s", a)

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
