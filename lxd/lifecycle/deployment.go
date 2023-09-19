package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// DeploymentAction represents a lifecycle event action for deployments.
type DeploymentAction string

// DeploymentKeyAction represents a lifecycle event action for deployment keys.
type DeploymentKeyAction string

// DeploymentInstanceAction represents a lifecycle event action for deployment instances.
type DeploymentInstanceAction string

// DeploymentShapeAction represents a lifecycle event action for deployment shapes.
type DeploymentShapeAction string

// All supported lifecycle events for deployments.
const (
	DeploymentCreated      = DeploymentAction(api.EventLifecycleDeploymentCreated)
	DeploymentRenamed      = DeploymentAction(api.EventLifecycleDeploymentRenamed)
	DeploymentUpdated      = DeploymentAction(api.EventLifecycleDeploymentUpdated)
	DeploymentDeleted      = DeploymentAction(api.EventLifecycleDeploymentDeleted)
	DeploymentKeyCreated   = DeploymentKeyAction(api.EventLifecycleDeploymentKeyCreated)
	DeploymentKeyRenamed   = DeploymentKeyAction(api.EventLifecycleDeploymentKeyRenamed)
	DeploymentKeyUpdated   = DeploymentKeyAction(api.EventLifecycleDeploymentKeyUpdated)
	DeploymentKeyDeleted   = DeploymentKeyAction(api.EventLifecycleDeploymentKeyDeleted)
	DeploymentShapeCreated = DeploymentShapeAction(api.EventLifecycleDeploymentShapeCreated)
	DeploymentShapeRenamed = DeploymentShapeAction(api.EventLifecycleDeploymentShapeRenamed)
	DeploymentShapeUpdated = DeploymentShapeAction(api.EventLifecycleDeploymentShapeUpdated)
	DeploymentShapeDeleted = DeploymentShapeAction(api.EventLifecycleDeploymentShapeDeleted)
)

// Event creates the lifecycle event for an action on deployment.
func (a DeploymentAction) Event(projectName string, deploymentName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on deployment key.
func (a DeploymentKeyAction) Event(projectName string, deploymentName string, deploymentKeyName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName, "keys", deploymentKeyName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on an instance deployment.
func (a DeploymentInstanceAction) Event(projectName string, deploymentName string, instSetName string, instName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName, "instance-sets", instSetName, "instances", instName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on a deployment shape.
func (a DeploymentShapeAction) Event(projectName string, deploymentName string, deploymentShapeName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "deployments", deploymentName, "shapes", deploymentShapeName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
