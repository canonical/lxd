package lxd

import (
	"github.com/canonical/lxd/shared/api"
)

// GetDeploymentNames returns a list of deployment names.
func (r *ProtocolLXD) GetDeploymentNames() ([]string, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("deployments")
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetDeployments returns a list of deployments structs.
func (r *ProtocolLXD) GetDeployments() ([]api.Deployment, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	deployments := []api.Deployment{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments").WithQuery("recursion", "1")
	_, err = r.queryStruct("GET", u.String(), nil, "", &deployments)
	if err != nil {
		return nil, err
	}

	return deployments, nil
}

// GetDeployment returns a Deployment entry for the provided name.
func (r *ProtocolLXD) GetDeployment(deploymentName string) (*api.Deployment, string, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, "", err
	}

	deployment := api.Deployment{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments", deploymentName)
	etag, err := r.queryStruct("GET", u.String(), nil, "", &deployment)
	if err != nil {
		return nil, "", err
	}

	return &deployment, etag, nil
}

// CreateDeployment defines a new deployment using the provided struct.
func (r *ProtocolLXD) CreateDeployment(deployment api.DeploymentsPost) error {
	err := r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments")
	_, _, err = r.query("POST", u.String(), deployment, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateDeployment updates the deployment to match the provided struct.
func (r *ProtocolLXD) UpdateDeployment(deploymentName string, deployment api.DeploymentPut, ETag string) error {
	err := r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName)
	_, _, err = r.query("PUT", u.String(), deployment, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameDeployment renames an existing deployment entry.
func (r *ProtocolLXD) RenameDeployment(deploymentName string, deployment api.DeploymentPost) error {
	err := r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName)
	_, _, err = r.query("POST", u.String(), deployment, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteDeployment deletes an existing deployment.
func (r *ProtocolLXD) DeleteDeployment(deploymentName string) error {
	err := r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName)
	_, _, err = r.query("DELETE", u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetDeploymentShapeNames returns a list of deployment shape names (URL) for the provided deployment.
func (r *ProtocolLXD) GetDeploymentShapeNames(deploymentName string) (deploymentShapeNames []string, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("deployments", deploymentName, "shapes")
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetDeploymentKeyNames returns a list of deployment key names (URL) for the provided deployment.
func (r *ProtocolLXD) GetDeploymentKeyNames(deploymentName string) (deploymentKeyNames []string, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("deployments", deploymentName, "keys")
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetDeploymentKeys returns a list of deployment key structs for the provided deployment.
func (r *ProtocolLXD) GetDeploymentKeys(deploymentName string) ([]api.DeploymentKey, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	deploymentKeys := []api.DeploymentKey{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments", deploymentName, "keys").WithQuery("recursion", "1")
	_, err = r.queryStruct("GET", u.String(), nil, "", &deploymentKeys)
	if err != nil {
		return nil, err
	}

	return deploymentKeys, nil
}

// GetDeploymentKey returns a deployment key struct for the provided deployment.
func (r *ProtocolLXD) GetDeploymentKey(deploymentName string, deploymentKeyName string) (*api.DeploymentKey, string, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, "", err
	}

	deploymentKey := api.DeploymentKey{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments", deploymentName, "keys", deploymentKeyName)
	etag, err := r.queryStruct("GET", u.String(), nil, "", &deploymentKey)
	if err != nil {
		return nil, "", err
	}

	return &deploymentKey, etag, nil
}

// CreateDeploymentKey creates a new deployment key and return the authentication information only once.
func (r *ProtocolLXD) CreateDeploymentKey(deploymentName string, deploymentKey api.DeploymentKeysPost) error {
	err := r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "keys")
	_, _, err = r.query("POST", u.String(), deploymentKey, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateDeploymentKey updates the deployment key.
func (r *ProtocolLXD) UpdateDeploymentKey(deploymentName string, deploymentKeyName string, deploymentKey api.DeploymentKeyPut, ETag string) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "keys", deploymentKeyName)
	_, _, err = r.query("PUT", u.String(), deploymentKey, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameDeploymentKey renames an existing deployment key.
func (r *ProtocolLXD) RenameDeploymentKey(deploymentName string, deploymentKeyName string, deploymentKey api.DeploymentKeyPost) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "keys", deploymentKeyName)
	_, _, err = r.query("POST", u.String(), deploymentKey, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteDeploymentKey deletes an existing deployment key.
func (r *ProtocolLXD) DeleteDeploymentKey(deploymentName string, deploymentKeyName string) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "keys", deploymentKeyName)
	_, _, err = r.query("DELETE", u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetDeploymentShapes returns a list of deployment shape structs for the provided deployment.
func (r *ProtocolLXD) GetDeploymentShapes(deploymentName string) (deploymentShapes []api.DeploymentShape, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	deploymentShapes = []api.DeploymentShape{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments", deploymentName, "shapes").WithQuery("recursion", "1")
	_, err = r.queryStruct("GET", u.String(), nil, "", &deploymentShapes)
	if err != nil {
		return nil, err
	}

	return deploymentShapes, nil
}

// GetDeploymentShape returns a deployment shape structs for the provided deployment.
func (r *ProtocolLXD) GetDeploymentShape(deploymentName string, deploymentShapeName string) (deploymentShape *api.DeploymentShape, ETag string, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, "", err
	}

	deploymentShape = &api.DeploymentShape{}

	// Fetch the raw value.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName)
	etag, err := r.queryStruct("GET", u.String(), nil, "", deploymentShape)
	if err != nil {
		return nil, "", err
	}

	return deploymentShape, etag, nil
}

// CreateDeploymentShape defines a new deployment shape.
func (r *ProtocolLXD) CreateDeploymentShape(deploymentName string, deploymentShape api.DeploymentShapesPost) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes")
	_, _, err = r.query("POST", u.String(), deploymentShape, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateDeploymentShape updates the deployment shape.
func (r *ProtocolLXD) UpdateDeploymentShape(deploymentName string, deploymentShapeName string, deploymentShape api.DeploymentShapePut, ETag string) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName)
	_, _, err = r.query("PUT", u.String(), deploymentShape, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameDeploymentShape renames an existing deployment shape.
func (r *ProtocolLXD) RenameDeploymentShape(deploymentName string, deploymentShapeName string, deploymentShape api.DeploymentShapePost) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName)
	_, _, err = r.query("POST", u.String(), deploymentShape, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteDeploymentShape deletes an existing deployment shape.
func (r *ProtocolLXD) DeleteDeploymentShape(deploymentName string, deploymentShapeName string) (err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName)
	_, _, err = r.query("DELETE", u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// AddInstanceToDeploymentShape adds an instance with a given instance name to a deployment shape.
func (r *ProtocolLXD) AddInstanceToDeploymentShape(deploymentName string, deploymentShapeInstance api.DeploymentInstancesPost) (RemoteOperation, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeInstance.ShapeName, "instances")
	op, _, err := r.queryOperation("POST", u.String(), deploymentShapeInstance, "", true)
	if err != nil {
		return nil, err
	}

	rop := remoteOperation{
		targetOp: op,
		chDone:   make(chan bool),
	}

	// Forward targetOp to remote op
	go func() {
		rop.err = rop.targetOp.Wait()
		close(rop.chDone)
	}()

	return &rop, nil
}

// DeleteInstanceInDeploymentShape deletes an instance with a given instance name from a deployment shape.
func (r *ProtocolLXD) DeleteInstanceInDeploymentShape(deploymentName string, deploymentShapeName string, instanceName string) (RemoteOperation, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName, "instances", instanceName)
	op, _, err := r.queryOperation("DELETE", u.String(), nil, "", true)
	if err != nil {
		return nil, err
	}

	rop := remoteOperation{
		targetOp: op,
		chDone:   make(chan bool),
	}

	// Forward targetOp to remote op
	go func() {
		rop.err = rop.targetOp.Wait()
		close(rop.chDone)
	}()

	return &rop, nil
}

// GetInstanceNamesInDeploymentShape returns a list of instance names (URL) for the provided deployment shape.
func (r *ProtocolLXD) GetInstanceNamesInDeploymentShape(deploymentName string, deploymentShapeName string) (instanceNames []string, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	instanceNames = []string{}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName, "instances")
	_, err = r.queryStruct("GET", u.String(), nil, "", &instanceNames)
	if err != nil {
		return nil, err
	}

	return instanceNames, nil
}

// GetInstancesInDeploymentShape returns a list of instance structs for the provided deployment shape.
func (r *ProtocolLXD) GetInstancesInDeploymentShape(deploymentName string, deploymentShapeName string) (instances []api.Instance, err error) {
	err = r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	instances = []api.Instance{}

	// Send the request.
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName, "instances").WithQuery("recursion", "1")
	_, err = r.queryStruct("GET", u.String(), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func (r *ProtocolLXD) UpdateDeploymentInstanceState(deploymentName string, deploymentShapeName string, instanceName string, instanceState api.InstanceStatePut, ETag string) (Operation, error) {
	err := r.CheckExtension("deployments")
	if err != nil {
		return nil, err
	}

	// Send the request
	u := api.NewURL().Path("deployments", deploymentName, "shapes", deploymentShapeName, "instances", instanceName, "state")
	op, _, err := r.queryOperation("PUT", u.String(), instanceState, ETag, true)
	if err != nil {
		return nil, err
	}

	return op, nil
}
