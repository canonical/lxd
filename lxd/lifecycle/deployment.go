package lifecycle

import (
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// DeploymentAction represents a lifecycle event action for deployments.
type DeploymentAction string

// DeploymentInstanceSetAction represents a lifecycle event action for deployment instance sets.
type DeploymentInstanceSetAction string

// All supported lifecycle events for deployments.
const (
	DeploymentCreated            = DeploymentAction(api.EventLifecycleDeploymentCreated)
	DeploymentRenamed            = DeploymentAction(api.EventLifecycleDeploymentRenamed)
	DeploymentUpdated            = DeploymentAction(api.EventLifecycleDeploymentUpdated)
	DeploymentDeleted            = DeploymentAction(api.EventLifecycleDeploymentDeleted)
	DeploymentInstanceSetCreated = DeploymentInstanceSetAction(api.EventLifecycleDeploymentInstanceSetCreated)
	DeploymentInstanceSetRenamed = DeploymentInstanceSetAction(api.EventLifecycleDeploymentInstanceSetRenamed)
	DeploymentInstanceSetUpdated = DeploymentInstanceSetAction(api.EventLifecycleDeploymentInstanceSetUpdated)
	DeploymentInstanceSetDeleted = DeploymentInstanceSetAction(api.EventLifecycleDeploymentInstanceSetDeleted)
)

// Event creates the lifecycle event for an action on a network acl.
func (a DeploymentAction) Event(projectName string, deploymentName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on a storage bucket.
func (a DeploymentInstanceSetAction) Event(projectName string, deploymentName string, instSetName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName, "instance-sets", instSetName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
