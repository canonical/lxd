package lifecycle

import (
	"fmt"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// Internal copy of the instance interface.
type instance interface {
	Name() string
	Project() string
	Operation() *operations.Operation
}

// InstanceAction represents a lifecycle event action for instances.
type InstanceAction string

// All supported lifecycle events for instances.
const (
	InstanceCreated          = InstanceAction("created")
	InstanceStarted          = InstanceAction("started")
	InstanceStopped          = InstanceAction("stopped")
	InstanceShutdown         = InstanceAction("shutdown")
	InstanceRestarted        = InstanceAction("restarted")
	InstancePaused           = InstanceAction("paused")
	InstanceResumed          = InstanceAction("resumed")
	InstanceRestored         = InstanceAction("restored")
	InstanceDeleted          = InstanceAction("deleted")
	InstanceRenamed          = InstanceAction("renamed")
	InstanceUpdated          = InstanceAction("updated")
	InstanceExec             = InstanceAction("exec")
	InstanceConsole          = InstanceAction("console")
	InstanceConsoleRetrieved = InstanceAction("console-retrieved")
	InstanceConsoleReset     = InstanceAction("console-reset")
	InstanceFileRetrieved    = InstanceAction("file-retrieved")
	InstanceFilePushed       = InstanceAction("file-pushed")
	InstanceFileDeleted      = InstanceAction("file-deleted")
)

// Event creates the lifecycle event for an action on an instance.
func (a InstanceAction) Event(inst instance, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("instance-%s", a)
	url := api.NewURL().Path(version.APIVersion, "instances", inst.Name()).Project(inst.Project())

	var requestor *api.EventLifecycleRequestor
	if inst.Operation() != nil {
		requestor = inst.Operation().Requestor()
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    url.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
