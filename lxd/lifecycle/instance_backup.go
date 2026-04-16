package lifecycle

import (
	"context"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
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
func (a InstanceBackupAction) Event(ctx context.Context, fullBackupName string, inst instance, eventCtx map[string]any) api.EventLifecycle {
	_, backupName, _ := api.GetParentAndSnapshotName(fullBackupName)

	u := api.NewURL().Path(version.APIVersion, "instances", inst.Name(), "backups", backupName).Project(inst.Project().Name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   eventCtx,
		Requestor: request.CreateRequestor(ctx),
	}
}
