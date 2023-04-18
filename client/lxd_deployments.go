package lxd

import (
	"github.com/lxc/lxd/shared/api"
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
