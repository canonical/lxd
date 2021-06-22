package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
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
	InstanceCreated   = InstanceAction("created")
	InstanceStarted   = InstanceAction("started")
	InstanceStopped   = InstanceAction("stopped")
	InstanceShutdown  = InstanceAction("shutdown")
	InstanceRestarted = InstanceAction("restarted")
	InstancePaused    = InstanceAction("paused")
	InstanceResumed   = InstanceAction("resumed")
	InstanceRestored  = InstanceAction("restored")
	InstanceDeleted   = InstanceAction("deleted")
	InstanceRenamed   = InstanceAction("renamed")
	InstanceUpdated   = InstanceAction("updated")
)

// Event creates the lifecycle event for an action on an instance.
func (a InstanceAction) Event(inst instance, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("instance-%s", a)
	u := fmt.Sprintf("/1.0/instances/%s", url.PathEscape(inst.Name()))

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
